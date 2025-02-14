// Copyright 2022 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package session_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pingcap/failpoint"
	"github.com/pingcap/tidb/config"
	"github.com/pingcap/tidb/testkit"
	"github.com/stretchr/testify/require"
	tikvutil "github.com/tikv/client-go/v2/util"
)

func TestNonTransactionalDeleteSharding(t *testing.T) {
	store, clean := createStorage(t)
	defer clean()
	tk := testkit.NewTestKit(t, store)
	tk.MustExec("set @@tidb_max_chunk_size=35")
	tk.MustExec("use test")

	tables := []string{
		"create table t(a int, b int, primary key(a, b) clustered)",
		"create table t(a int, b int, primary key(a, b) nonclustered)",
		"create table t(a int, b int, primary key(a) clustered)",
		"create table t(a int, b int, primary key(a) nonclustered)",
		"create table t(a int, b int, key(a, b))",
		"create table t(a int, b int, key(a))",
		"create table t(a int, b int, unique key(a, b))",
		"create table t(a int, b int, unique key(a))",
		"create table t(a varchar(30), b int, primary key(a, b) clustered)",
		"create table t(a varchar(30), b int, primary key(a, b) nonclustered)",
		"create table t(a varchar(30), b int, primary key(a) clustered)",
		"create table t(a varchar(30), b int, primary key(a) nonclustered)",
		"create table t(a varchar(30), b int, key(a, b))",
		"create table t(a varchar(30), b int, key(a))",
		"create table t(a varchar(30), b int, unique key(a, b))",
		"create table t(a varchar(30), b int, unique key(a))",
	}
	tableSizes := []int{0, 1, 10, 35, 40, 100}
	batchSizes := []int{1, 10, 25, 35, 50, 80, 120}
	for _, table := range tables {
		tk.MustExec("drop table if exists t")
		tk.MustExec(table)
		for _, tableSize := range tableSizes {
			for _, batchSize := range batchSizes {
				for i := 0; i < tableSize; i++ {
					tk.MustExec(fmt.Sprintf("insert into t values ('%d', %d)", i, i*2))
				}
				tk.MustQuery(fmt.Sprintf("split on a limit %d delete from t", batchSize)).Check(testkit.Rows(fmt.Sprintf("%d all succeeded", (tableSize+batchSize-1)/batchSize)))
				tk.MustQuery("select count(*) from t").Check(testkit.Rows("0"))
			}
		}
	}
}

func TestNonTransactionalDeleteDryRun(t *testing.T) {
	store, clean := createStorage(t)
	defer clean()
	tk := testkit.NewTestKit(t, store)
	tk.MustExec("set @@tidb_max_chunk_size=35")
	tk.MustExec("use test")
	tk.MustExec("create table t(a int, b int, primary key(a, b) clustered)")
	for i := 0; i < 100; i++ {
		tk.MustExec(fmt.Sprintf("insert into t values ('%d', %d)", i, i*2))
	}
	rows := tk.MustQuery("split on a limit 3 dry run delete from t").Rows()
	for _, row := range rows {
		require.True(t, strings.HasPrefix(row[0].(string), "DELETE FROM `test`.`t` WHERE `a` BETWEEN"))
	}
	tk.MustQuery("split on a limit 3 dry run query delete from t").Check(testkit.Rows(
		"SELECT `a` FROM `test`.`t` WHERE TRUE ORDER BY IF(ISNULL(`a`),0,1),`a`"))
	tk.MustQuery("select count(*) from t").Check(testkit.Rows("100"))
}

func TestNonTransactionalDeleteErrorMessage(t *testing.T) {
	store, clean := createStorage(t)
	defer clean()
	tk := testkit.NewTestKit(t, store)
	tk.MustExec("set @@tidb_max_chunk_size=35")
	tk.MustExec("use test")
	tk.MustExec("create table t(a int, b int, primary key(a, b) clustered)")
	for i := 0; i < 100; i++ {
		tk.MustExec(fmt.Sprintf("insert into t values ('%d', %d)", i, i*2))
	}
	failpoint.Enable("github.com/pingcap/tidb/session/splitDeleteError", `return`)
	defer failpoint.Disable("github.com/pingcap/tidb/session/splitDeleteError")
	rows := tk.MustQuery("split on a limit 3 delete from t").Rows()
	require.Equal(t, 1, len(rows))
	require.Equal(t, rows[0][2].(string), "Early return: error occurred in the first job. All jobs are canceled: injected split delete error")
}

func TestNonTransactionalDeleteSplitOnTiDBRowID(t *testing.T) {
	store, clean := createStorage(t)
	defer clean()
	tk := testkit.NewTestKit(t, store)
	tk.MustExec("set @@tidb_max_chunk_size=35")
	tk.MustExec("use test")
	tk.MustExec("create table t(a int, b int)")
	for i := 0; i < 100; i++ {
		tk.MustExec(fmt.Sprintf("insert into t values ('%d', %d)", i, i*2))
	}
	tk.MustExec("split on _tidb_rowid limit 3 delete from t")
	tk.MustQuery("select count(*) from t").Check(testkit.Rows("0"))
}

func TestNonTransactionalDeleteNull(t *testing.T) {
	store, clean := createStorage(t)
	defer clean()
	tk := testkit.NewTestKit(t, store)
	tk.MustExec("set @@tidb_max_chunk_size=35")
	tk.MustExec("use test")
	tk.MustExec("create table t(a int, b int, key(a))")
	for i := 0; i < 100; i++ {
		tk.MustExec(fmt.Sprintf("insert into t values ('%d', %d)", i, i*2))
		tk.MustExec("insert into t values (null, null)")
	}

	tk.MustExec("split on a limit 3 delete from t")
	tk.MustQuery("select count(*) from t").Check(testkit.Rows("0"))

	// all values are null
	for i := 0; i < 100; i++ {
		tk.MustExec("insert into t values (null, null)")
	}
	tk.MustExec("split on a limit 3 delete from t")
	tk.MustQuery("select count(*) from t").Check(testkit.Rows("0"))
}

func TestNonTransactionalDeleteSmallBatch(t *testing.T) {
	store, clean := createStorage(t)
	defer clean()
	tk := testkit.NewTestKit(t, store)
	tk.MustExec("set @@tidb_max_chunk_size=1024")
	tk.MustExec("use test")
	tk.MustExec("create table t(a int, b int, key(a))")
	for i := 0; i < 10; i++ {
		tk.MustExec(fmt.Sprintf("insert into t values ('%d', %d)", i, i*2))
		tk.MustExec("insert into t values (null, null)")
	}
	require.Equal(t, 1, len(tk.MustQuery("split on a limit 1000 dry run delete from t").Rows()))
	tk.MustExec("split on a limit 1000 delete from t")
	tk.MustQuery("select count(*) from t").Check(testkit.Rows("0"))
}

func TestNonTransactionalDeleteShardOnGeneratedColumn(t *testing.T) {
	store, clean := createStorage(t)
	defer clean()
	tk := testkit.NewTestKit(t, store)
	tk.MustExec("set @@tidb_max_chunk_size=35")
	tk.MustExec("use test")
	tk.MustExec("create table t(a int, b int, c double as (sqrt(a * a + b * b)), key(c))")
	for i := 0; i < 1000; i++ {
		tk.MustExec(fmt.Sprintf("insert into t values (%d, %d, default)", i, i*2))
	}
	tk.MustExec("split on c limit 10 delete from t")
	tk.MustQuery("select count(*) from t").Check(testkit.Rows("0"))
}

func TestNonTransactionalDeleteAutoDetectShardColumn(t *testing.T) {
	store, clean := createStorage(t)
	defer clean()
	tk := testkit.NewTestKit(t, store)
	tk.MustExec("set @@tidb_max_chunk_size=35")
	tk.MustExec("use test")

	goodTables := []string{
		"create table t(a int, b int)",
		"create table t(a int, b int, primary key(a) clustered)",
		"create table t(a int, b int, primary key(a) nonclustered)",
		"create table t(a int, b int, primary key(a, b) nonclustered)",
		"create table t(a varchar(30), b int, primary key(a) clustered)",
		"create table t(a varchar(30), b int, primary key(a, b) nonclustered)",
	}
	badTables := []string{
		"create table t(a int, b int, primary key(a, b) clustered)",
		"create table t(a varchar(30), b int, primary key(a, b) clustered)",
	}

	testFunc := func(table string, expectSuccess bool) {
		tk.MustExec("drop table if exists t")
		tk.MustExec(table)
		for i := 0; i < 100; i++ {
			tk.MustExec(fmt.Sprintf("insert into t values ('%d', %d)", i, i*2))
		}
		_, err := tk.Exec("split limit 3 delete from t")
		require.Equal(t, expectSuccess, err == nil)
	}

	for _, table := range goodTables {
		testFunc(table, true)
	}
	for _, table := range badTables {
		testFunc(table, false)
	}
}

func TestNonTransactionalDeleteInvisibleIndex(t *testing.T) {
	store, clean := createStorage(t)
	defer clean()
	tk := testkit.NewTestKit(t, store)
	tk.MustExec("set @@tidb_max_chunk_size=35")
	tk.MustExec("use test")
	tk.MustExec("create table t(a int, b int)")
	for i := 0; i < 100; i++ {
		tk.MustExec(fmt.Sprintf("insert into t values (%d, %d)", i, i*2))
	}
	err := tk.ExecToErr("split on a limit 10 delete from t")
	require.Error(t, err)
	tk.MustExec("CREATE UNIQUE INDEX c1 ON t (a) INVISIBLE")
	err = tk.ExecToErr("split on a limit 10 delete from t")
	require.Error(t, err)
	tk.MustExec("CREATE UNIQUE INDEX c2 ON t (a)")
	tk.MustExec("split on a limit 10 delete from t")
	tk.MustQuery("select count(*) from t").Check(testkit.Rows("0"))
}

func TestNonTransactionalDeleteIgnoreSelectLimit(t *testing.T) {
	store, clean := createStorage(t)
	defer clean()
	tk := testkit.NewTestKit(t, store)
	tk.MustExec("set @@tidb_max_chunk_size=35")
	tk.MustExec("set @@sql_select_limit=3")
	tk.MustExec("use test")
	tk.MustExec("create table t(a int, b int, key(a))")
	for i := 0; i < 100; i++ {
		tk.MustExec(fmt.Sprintf("insert into t values (%d, %d)", i, i*2))
	}
	tk.MustExec("split on a limit 10 delete from t")
	tk.MustQuery("select count(*) from t").Check(testkit.Rows("0"))
}

func TestNonTransactionalDeleteReadStaleness(t *testing.T) {
	store, clean := createStorage(t)
	defer clean()
	tk := testkit.NewTestKit(t, store)
	tk.MustExec("set @@tidb_max_chunk_size=35")
	tk.MustExec("set @@tidb_read_staleness=-100")
	tk.MustExec("use test")
	tk.MustExec("create table t(a int, b int, key(a))")
	for i := 0; i < 100; i++ {
		tk.MustExec(fmt.Sprintf("insert into t values (%d, %d)", i, i*2))
	}
	tk.MustExec("split on a limit 10 delete from t")
	tk.MustExec("set @@tidb_read_staleness=0")
	tk.MustQuery("select count(*) from t").Check(testkit.Rows("0"))
}

func TestNonTransactionalDeleteCheckConstraint(t *testing.T) {
	store, clean := createStorage(t)
	defer clean()
	tk := testkit.NewTestKit(t, store)

	tk.MustExec("use test")
	tk.MustExec("create table t(a int, b int, key(a))")

	// For mocked tikv, safe point is not initialized, we manually insert it for snapshot to use.
	safePointName := "tikv_gc_safe_point"
	now := time.Now()
	safePointValue := now.Format(tikvutil.GCTimeFormat)
	safePointComment := "All versions after safe point can be accessed. (DO NOT EDIT)"
	updateSafePoint := fmt.Sprintf("INSERT INTO mysql.tidb VALUES ('%[1]s', '%[2]s', '%[3]s') ON DUPLICATE KEY UPDATE variable_value = '%[2]s', comment = '%[3]s'", safePointName, safePointValue, safePointComment)
	tk.MustExec(updateSafePoint)

	tk.MustExec("set @@tidb_max_chunk_size=35")
	tk.MustExec("set @a=now(6)")

	for i := 0; i < 100; i++ {
		tk.MustExec(fmt.Sprintf("insert into t values (%d, %d)", i, i*2))
	}
	tk.MustExec("set @@tidb_snapshot=@a")
	err := tk.ExecToErr("split on a limit 10 delete from t")
	require.Error(t, err)
	tk.MustExec("set @@tidb_snapshot=''")
	tk.MustQuery("select count(*) from t").Check(testkit.Rows("100"))

	tk.MustExec("set @@tidb_read_consistency=weak")
	err = tk.ExecToErr("split on a limit 10 delete from t")
	require.Error(t, err)
	tk.MustQuery("select count(*) from t").Check(testkit.Rows("100"))
	tk.MustExec("set @@tidb_read_consistency=strict")

	tk.MustExec("set autocommit=0")
	err = tk.ExecToErr("split on a limit 10 delete from t")
	require.Error(t, err)
	tk.MustQuery("select count(*) from t").Check(testkit.Rows("100"))
	tk.MustExec("set autocommit=1")

	tk.MustExec("begin")
	err = tk.ExecToErr("split on a limit 10 delete from t")
	require.Error(t, err)
	tk.MustQuery("select count(*) from t").Check(testkit.Rows("100"))
	tk.MustExec("commit")

	config.GetGlobalConfig().EnableBatchDML = true
	tk.Session().GetSessionVars().BatchInsert = true
	tk.Session().GetSessionVars().DMLBatchSize = 1
	err = tk.ExecToErr("split on a limit 10 delete from t")
	require.Error(t, err)
	tk.MustQuery("select count(*) from t").Check(testkit.Rows("100"))
	config.GetGlobalConfig().EnableBatchDML = false
	tk.Session().GetSessionVars().BatchInsert = false
	tk.Session().GetSessionVars().DMLBatchSize = 0

	tk.MustExec("create table t1(a int, b int, key(a))")
	tk.MustExec("insert into t1 values (1, 1)")
	err = tk.ExecToErr("split limit 1 delete t, t1 from t, t1 where t.a = t1.a")
	require.Error(t, err)
	tk.MustQuery("select count(*) from t").Check(testkit.Rows("100"))
	tk.MustQuery("select count(*) from t1").Check(testkit.Rows("1"))
}

func TestNonTransactionalDeleteOptimizerHints(t *testing.T) {
	store, clean := createStorage(t)
	defer clean()
	tk := testkit.NewTestKit(t, store)
	tk.MustExec("use test")
	tk.MustExec("create table t(a int, b int, key(a))")
	for i := 0; i < 10; i++ {
		tk.MustExec(fmt.Sprintf("insert into t values ('%d', %d)", i, i*2))
	}
	result := tk.MustQuery("split on a limit 10 dry run delete /*+ USE_INDEX(t) */ from t").Rows()[0][0].(string)
	require.Equal(t, result, "DELETE /*+ USE_INDEX(`t` )*/ FROM `test`.`t` WHERE `a` BETWEEN 0 AND 9")
}
