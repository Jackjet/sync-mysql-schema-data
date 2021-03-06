package internal

import (
	"fmt"
	"log"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// SchemaSync 配置文件
type SchemaSync struct {
	Config   *Config
	SourceDb *MyDb
	DestDb   *MyDb
}

// NewSchemaSync 对一个配置进行同步
func NewSchemaSync(config *Config) *SchemaSync {
	s := new(SchemaSync)
	s.Config = config
	s.SourceDb = NewMyDb(config.SourceDSN, "source")
	s.DestDb = NewMyDb(config.DestDSN, "dest")
	return s
}

// GetNewTableNames 获取所有新增加的表名
func (sc *SchemaSync) GetNewTableNames() []string {
	sourceTables := sc.SourceDb.GetTableNames()
	destTables := sc.DestDb.GetTableNames()

	var newTables []string

	for _, name := range sourceTables {
		if !inStringSlice(name, destTables) {
			newTables = append(newTables, name)
		}
	}
	return newTables
}

func (sc *SchemaSync) getAlterDataByTable(table string) *TableAlterData {
	alter := new(TableAlterData)
	alter.Table = table
	alter.Type = alterTypeNo

	sschema := sc.SourceDb.GetTableSchema(table)
	dschema := sc.DestDb.GetTableSchema(table)

	alter.SchemaDiff = newSchemaDiff(table, sschema, dschema)

	if sschema == dschema {
		return alter
	}
	if sschema == "" {
		alter.Type = alterTypeDrop
		alter.SQL = fmt.Sprintf("drop table `%s`;", table)
		return alter
	}
	if dschema == "" {
		alter.Type = alterTypeCreate
		alter.SQL = sschema + ";"
		return alter
	}

	diff := sc.getSchemaDiff(alter)
	if diff != "" {
		alter.Type = alterTypeAlter
		alter.SQL = fmt.Sprintf("ALTER TABLE `%s`\n%s;", table, diff)
	}

	return alter
}

func (sc *SchemaSync) getSchemaDiff(alter *TableAlterData) string {
	sourceMyS := alter.SchemaDiff.Source
	destMyS := alter.SchemaDiff.Dest
	table := alter.Table

	var alterLines []string
	//比对字段
	for name, dt := range sourceMyS.Fields {
		if sc.Config.IsIgnoreField(table, name) {
			log.Printf("ignore column %s.%s", table, name)
			continue
		}
		var alterSQL string
		if destDt, has := destMyS.Fields[name]; has {
			if dt != destDt {
				alterSQL = fmt.Sprintf("CHANGE `%s` %s", name, dt)
			}
		} else {
			alterSQL = "ADD " + dt
		}
		if alterSQL != "" {
			//log.Println("trace check column.alter ", fmt.Sprintf("%s.%s", table, name), "alterSQL=", alterSQL)
			alterLines = append(alterLines, alterSQL)
		} else {
			//log.Println("trace check column.alter ", fmt.Sprintf("%s.%s", table, name), "not change")
		}
	}

	//源库已经删除的字段
	if sc.Config.Drop {
		for name := range destMyS.Fields {
			if sc.Config.IsIgnoreField(table, name) {
				log.Printf("ignore column %s.%s", table, name)
				continue
			}
			if _, has := sourceMyS.Fields[name]; !has {
				alterSQL := fmt.Sprintf("drop `%s`", name)
				alterLines = append(alterLines, alterSQL)
				//log.Println("trace check column.drop ", fmt.Sprintf("%s.%s", table, name), "alterSQL=", alterSQL)
			} else {
				//log.Println("trace check column.drop ", fmt.Sprintf("%s.%s", table, name), "not change")
			}
		}
	}

	//多余的字段暂不删除

	//比对索引
	for indexName, idx := range sourceMyS.IndexAll {
		if sc.Config.IsIgnoreIndex(table, indexName) {
			log.Printf("ignore index %s.%s", table, indexName)
			continue
		}
		dIdx, has := destMyS.IndexAll[indexName]
		//log.Println("trace indexName---->[", fmt.Sprintf("%s.%s", table, indexName), "] dest_has:", has, "\ndest_idx:", dIdx, "\nsource_idx:", idx)
		alterSQL := ""
		if has {
			if idx.SQL != dIdx.SQL {
				alterSQL = idx.alterAddSQL(true)
			}
		} else {
			alterSQL = idx.alterAddSQL(false)
		}
		if alterSQL != "" {
			alterLines = append(alterLines, alterSQL)
			//log.Println("trace check index.alter ", fmt.Sprintf("%s.%s", table, indexName), "alterSQL=", alterSQL)
		} else {
			//log.Println("trace check index.alter ", fmt.Sprintf("%s.%s", table, indexName), "not change")
		}
	}

	//drop index
	if sc.Config.Drop {
		for indexName, dIdx := range destMyS.IndexAll {
			if sc.Config.IsIgnoreIndex(table, indexName) {
				log.Printf("ignore index %s.%s", table, indexName)
				continue
			}
			var dropSQL string
			if _, has := sourceMyS.IndexAll[indexName]; !has {
				dropSQL = dIdx.alterDropSQL()
			}

			if dropSQL != "" {
				alterLines = append(alterLines, dropSQL)
				log.Println("trace check index.drop ", fmt.Sprintf("%s.%s", table, indexName), "alterSQL=", dropSQL)
			} else {
				log.Println("trace check index.drop ", fmt.Sprintf("%s.%s", table, indexName), " not change")
			}
		}
	}

	//比对外键
	for foreignName, idx := range sourceMyS.ForeignAll {
		if sc.Config.IsIgnoreForeignKey(table, foreignName) {
			log.Printf("ignore foreignName %s.%s", table, foreignName)
			continue
		}
		dIdx, has := destMyS.ForeignAll[foreignName]
		log.Println("trace foreignName---->[", fmt.Sprintf("%s.%s", table, foreignName), "] dest_has:", has, "\ndest_idx:", dIdx, "\nsource_idx:", idx)
		alterSQL := ""
		if has {
			if idx.SQL != dIdx.SQL {
				alterSQL = idx.alterAddSQL(true)
			}
		} else {
			alterSQL = idx.alterAddSQL(false)
		}
		if alterSQL != "" {
			alterLines = append(alterLines, alterSQL)
			//log.Println("trace check foreignKey.alter ", fmt.Sprintf("%s.%s", table, foreignName), "alterSQL=", alterSQL)
		} else {
			//log.Println("trace check foreignKey.alter ", fmt.Sprintf("%s.%s", table, foreignName), "not change")
		}
	}

	//drop 外键
	if sc.Config.Drop {
		for foreignName, dIdx := range destMyS.ForeignAll {
			if sc.Config.IsIgnoreForeignKey(table, foreignName) {
				log.Printf("ignore foreignName %s.%s", table, foreignName)
				continue
			}
			var dropSQL string
			if _, has := sourceMyS.ForeignAll[foreignName]; !has {
				log.Println("trace foreignName --->[", fmt.Sprintf("%s.%s", table, foreignName), "]", "didx:", dIdx)
				dropSQL = dIdx.alterDropSQL()

			}
			if dropSQL != "" {
				alterLines = append(alterLines, dropSQL)
				//log.Println("trace check foreignKey.drop ", fmt.Sprintf("%s.%s", table, foreignName), "alterSQL=", dropSQL)
			} else {
				//log.Println("trace check foreignKey.drop ", fmt.Sprintf("%s.%s", table, foreignName), "not change")
			}
		}
	}

	return strings.Join(alterLines, ",\n")
}

// SyncSQL4Dest sync schema change
func (sc *SchemaSync) SyncSQL4Dest(sqlStr string, sqls []string) error {
	log.Println("Exec_SQL_START:\n>>>>>>\n", sqlStr, "\n<<<<<<<<\n")
	sqlStr = strings.TrimSpace(sqlStr)
	if sqlStr == "" {
		log.Println("sql_is_empty,skip")
		return nil
	}
	t := newMyTimer()
	ret, err := sc.DestDb.Query(sqlStr)

	//how to enable allowMultiQueries?
	if err != nil && len(sqls) > 1 {
		log.Println("exec_mut_query failed,err=", err, ",now exec sqls foreach")
		tx, errTx := sc.DestDb.Db.Begin()
		if errTx == nil {
			for _, sql := range sqls {
				ret, err = tx.Query(sql)
				log.Println("query_one:[", sql, "]", err)
				if err != nil {
					break
				}
			}
			if err == nil {
				err = tx.Commit()
			} else {
				tx.Rollback()
			}
		}
	}
	t.stop()
	if err != nil {
		log.Println("EXEC_SQL_FAIELD", err)
		return err
	}
	log.Println("EXEC_SQL_SUCCESS,used:", t.usedSecond())
	cl, err := ret.Columns()
	log.Println("EXEC_SQL_RET:", cl, err)
	return err
}

// CheckSchemaDiff 执行最终的diff
func CheckSchemaDiff(cfg *Config) {
	statics := newStatics(cfg)
	defer (func() {
		statics.timer.stop()
		statics.sendMailNotice(cfg)
	})()

	sc := NewSchemaSync(cfg)
	newTables := sc.SourceDb.GetTableNames()
	log.Println("source db table total:", len(newTables))

	changedTables := make(map[string][]*TableAlterData)

	for _, table := range newTables {
		//log.Printf("Index : %d Table : %s\n", index, table)
		if !cfg.CheckMatchTables(table) {
			//log.Println("Table:", table, "skip")
			continue
		}

		if cfg.CheckMatchIgnoreTables(table) == true {
			//log.Println("Table:", table, "skip")
			continue
		}

		sd := sc.getAlterDataByTable(table)

		if sd.Type != alterTypeNo {
			fmt.Println(sd)
			fmt.Println("")
			relationTables := sd.SchemaDiff.RelationTables()
			//			fmt.Println("relationTables:",table,relationTables)

			//将所有有外键关联的单独放
			groupKey := "multi"
			if len(relationTables) == 0 {
				groupKey = "single_" + table
			}
			if _, has := changedTables[groupKey]; !has {
				changedTables[groupKey] = make([]*TableAlterData, 0)
			}
			changedTables[groupKey] = append(changedTables[groupKey], sd)
		} else {
			//log.Println("table:", table, "not change,", sd)
		}
	}

	//log.Println("trace changedTables:", changedTables)

	countSuccess := 0
	countFailed := 0
	canRunTypePref := "single"
	//先执行单个表的
run_sync:
	for typeName, sds := range changedTables {
		if !strings.HasPrefix(typeName, canRunTypePref) {
			continue
		}
		log.Println("runSyncType:", typeName)
		var sqls []string
		var sts []*tableStatics
		for _, sd := range sds {
			sql := strings.TrimRight(sd.SQL, ";")
			sqls = append(sqls, sql)

			st := statics.newTableStatics(sd.Table, sd)
			sts = append(sts, st)
		}

		sql := strings.Join(sqls, ";\n") + ";"
		var ret error

		if sc.Config.Sync {

			ret = sc.SyncSQL4Dest(sql, sqls)
			if ret == nil {
				countSuccess++
			} else {
				countFailed++
			}
		}
		for _, st := range sts {
			st.alterRet = ret
			st.schemaAfter = sc.DestDb.GetTableSchema(st.table)
			st.timer.stop()
		}

	} //end for

	//最后再执行多个表的alter
	if canRunTypePref == "single" {
		canRunTypePref = "multi"
		goto run_sync
	}

	if sc.Config.Sync {
		log.Println("execute_all_sql_done,success_total:", countSuccess, "failed_total:", countFailed)
	}

}

// 把配置里面的表的数据 同步到目标数据库
func SyncTableData(cfg *Config) {
	statics := newStatics(cfg)
	defer (func() {
		statics.timer.stop()
	})()
	sc := NewSchemaSync(cfg)

	needSyncDataTables := cfg.SyncDataTables
	log.Println("[SyncTableData] tables:", needSyncDataTables)
	if len(needSyncDataTables) <= 0 {
		log.Println("[SyncTableData] no tables need sync")
		return
	}

	// 源数据库所有的表
	allSourceTables := sc.SourceDb.GetTableNames()
	needSyncDataTablesOk := []string{}
	for _, tableTmp := range allSourceTables {
		if cfg.CheckMatchSyncTables(tableTmp) == false {
			continue
		}
		needSyncDataTablesOk = append(needSyncDataTablesOk, tableTmp)
	}

	// 每次同步多少条
	var limitNum float64 = 100
	for _, oneTable := range needSyncDataTablesOk {
		if cfg.CheckMatchIgnoreTables(oneTable) == true {
			log.Println("[SyncTableData] ignore table:", oneTable)
			continue
		}
		if cfg.SyncDataTruncate == true {
			_, err := sc.DestDb.Db.Exec("truncate  table " + oneTable)
			if err != nil {
				log.Println("[SyncTableData] truncate table error :", oneTable, " error:", err)
				continue
			}
			log.Println("[SyncTableData] truncate table:", oneTable)
		}
		//查询数据表 是否自增
		hasAutoIncrement := true
		sqlTableStatus := fmt.Sprintf("show  table  status where  Name='%s'", oneTable)
		tableStatusData := sc.DestDb.QueryAll(sqlTableStatus)
		if len(tableStatusData) == 0 {
			log.Println("[SyncTableData] show table status error:", sqlTableStatus)
			continue
		}
		autoIncrement := tableStatusData[0]["Auto_increment"]
		dataType := reflect.TypeOf(autoIncrement)
		if dataType == nil {
			hasAutoIncrement = false
		}
		// 查询总行数
		sqlCount := fmt.Sprintf("select count(1) as total_num from %s", oneTable)
		rsCount := sc.SourceDb.Db.QueryRow(sqlCount)
		var totalNum float64 = 0
		rsCount.Scan(&totalNum)

		totalTimes := math.Ceil(totalNum / limitNum)
		var limitStart, i float64 = 0, 0
		okNum := 0
		for ; i < totalTimes; i++ {
			limitStart = i * limitNum
			sql := fmt.Sprintf("select * from %s limit %v,%v", oneTable, limitStart, limitNum)
			valObjs := sc.SourceDb.QueryAll(sql)
			for _, valObj := range valObjs {
				insertSql := buildInsertSql(oneTable, valObj, cfg.SyncDataTruncate, hasAutoIncrement)
				insertResult, insertErr := sc.DestDb.Db.Exec(insertSql)
				if insertResult == nil || insertErr != nil {
					log.Println("[SyncTableData] insert error:", insertErr, " table:", oneTable, "sql:", insertSql)
					continue
				}
				insertId, insertErr := insertResult.LastInsertId()
				insertAffectedNum, _ := insertResult.RowsAffected()
				if (insertId == 0 && insertAffectedNum == 0) || insertErr != nil {
					log.Println("[SyncTableData] insert error:", insertErr, " insertId:", insertId, " insertAffectedNum:", insertAffectedNum, " table:", oneTable)
					continue
				}
				okNum++
			}
		}

		log.Println("[SyncTableData] table :", oneTable, " totalNum:", totalNum, " okNum:", okNum)
	}
}

func buildInsertSql(tableName string, insertTmp map[string]interface{}, truncate bool, hasAutoIncrement bool) string {
	sortedKeys := make([]string, 0)
	for k, _ := range insertTmp {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)
	fieldOk, valueOk, sql := "", "", ""
	totalNum := len(insertTmp)
	suffix := ","
	for num, k := range sortedKeys {
		if totalNum-1 == num {
			fieldOk += "`" + k + "`"
			suffix = ""
		} else {
			fieldOk += "`" + k + "`,"
		}
		v := insertTmp[k]
		switch v.(type) {
		case int:
			if truncate == false && k == "id" && hasAutoIncrement == true {
				valueOk += "null" + suffix
			} else {
				tmpValue := Int2Str(v.(int))
				valueOk += tmpValue + suffix
			}
		case int64:
			if truncate == false && k == "id" && hasAutoIncrement == true {
				valueOk += "null" + suffix
			} else {
				tmpValue := Int642Str(v.(int64))
				valueOk += tmpValue + suffix
			}
		case float64:
			tmpValue := Float642Str(v.(float64))
			valueOk += tmpValue + suffix
		case float32:
			tmpValue := Float322Str(v.(float32))
			valueOk += tmpValue + suffix
		case string:
			if truncate == false && k == "id" && hasAutoIncrement == true {
				valueOk += "null" + suffix
			} else {
				valueOk += "'" + strings.Replace(v.(string), "'", `\'`, -1) + "'" + suffix
			}
		case nil:
			valueOk += "NULL" + suffix
		}
	}

	sql += "insert into " + tableName + "(" + fieldOk + ") values (" + valueOk + ");"
	return sql
}

func Str2Int64(str string) (int64, error) {
	number, err := strconv.ParseInt(str, 10, 64)
	return number, err
}

func Int642Str(number int64) string {
	return strconv.FormatInt(number, 10)
}

func Str2Int(str string) (int, error) {
	number, err := strconv.ParseInt(str, 10, 0)
	return int(number), err
}

func Int2Str(number int) string {
	return strconv.FormatInt(int64(number), 10)
}

func Float2Str(f float32) string {
	return Float642Str(float64(f))
}

func Float642Str(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func Float322Str(f float32) string {
	return strconv.FormatFloat(float64(f), 'f', -1, 32)
}

func Str2Float64(s string) (f float64, err error) {
	f, err = strconv.ParseFloat(s, 64)

	return
}

func Str2Float(s string) (f float32, err error) {
	f64, err := Str2Float64(s)
	f = float32(f64)

	return
}
