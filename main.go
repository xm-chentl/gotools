package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/xm-chentl/goresource/mysqlex"
)

const (
	DateTimeLayout = "20060102150405"
)

var (
	templateOfBaseModel = `package global

type BaseModel struct {
	ID int {{.tag}}
}
	
func (m BaseModel) GetID() interface{} {
	return m.ID
}
	
func (m *BaseModel) SetID(v interface{}) {
	if v == nil {
		return
	}
	
	m.ID = v.(int)
}`

	templateOfModel = `package global

{{if gt (len .packages) 0}}
import (
	{{range .packages}}"{{.}}"
	{{end}}
)
{{end}}

type {{.model}} struct {
	{{.baseModel}}
	
	{{range .fields}}{{.Name}} {{.Type}}  {{.GetTag}} // {{.Name}} {{.Comment}}
	{{end}}
}
	
func (m {{.model}}) Table() string {
	return "{{.table}}"
}
	
func (m {{.model}}) TableName() string {
	return m.Table()
}`
	mappingType = map[string]string{
		"int":       "int",
		"varchar":   "string",
		"text":      "string",
		"bigint":    "int64",
		"smallint":  "int",
		"tinyint":   "int",
		"double":    "float64",
		"date":      "time.Time",
		"decimal":   "float64",
		"json":      "string",
		"datetime":  "time.Time",
		"longtext":  "string",
		"timestamp": "int64",
	}
)

// Tables 获取所有表
type Database string

// Table 数据库查询出来的表信息
type Table struct {
	Name          string
	ColumnName    string
	ColumnDefault string
	IsNull        string
	DataType      string
	ColumnKey     string
	ColumnComment string
	DataLength    int
}

// Field 字段信息
type Field struct {
	Name         string
	Type         string
	ColumnName   string
	ColumnType   string
	Comment      string
	Default      string
	ColumnLength int
	IsNull       bool
	IsPrimaryKey bool
}

func (f Field) GetTag() string {
	return fmt.Sprintf("`gorm:\"column:%s;type:%s;size:%d;comment:%s\"`",
		f.ColumnName,
		f.ColumnType,
		f.ColumnLength,
		// todo: 临时处理 \ > /
		strings.ReplaceAll(strings.ReplaceAll(f.Comment, "\"", "'"), `\`, "/"),
	)
}

func main() {
	dsn := "root:123456@tcp(10.1.1.101)/cp_server?charset=utf8mb4"
	mysqlDb := mysqlex.New(dsn)
	ctx := context.TODO()
	db := mysqlDb.Db(ctx)
	var database Database
	if err := db.Query().Exec(&database, "select database()"); err != nil {
		panic(err)
	}

	tables := make([]Table, 0)
	sqlOfTables := `SELECT
		table_name as Name,
		column_name as ColumnName,
		column_default as ColumnDefault,
		is_nullable as IsNull,
		data_type as DataType,
		character_maximum_length as DataLength,
		column_key as ColumnKey,
		column_comment as ColumnComment
	FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? 
	ORDER BY table_name, ordinal_position
	`
	if err := db.Query().Exec(&tables, sqlOfTables, database); err != nil {
		return
	}
	tableOfFields := make(map[string][]Field)
	tableOfBaseModel := make(map[string]struct{})
	tableOfName := make(map[string]string)
	tableOfPackages := make(map[string]map[string]struct{})
	for _, table := range tables {
		if _, ok := tableOfFields[table.Name]; !ok {
			tableOfFields[table.Name] = make([]Field, 0)
			tableOfPackages[table.Name] = make(map[string]struct{}, 0)
		}
		tableOfName[table.Name] = getName(table.Name)
		dataType := getDataType(table.DataType)
		if dataType == "time.Time" {
			tableOfPackages[table.Name]["time"] = struct{}{}
		}
		if strings.EqualFold(table.ColumnName, "id") {
			tableOfBaseModel[table.Name] = struct{}{}
			continue
		}
		tableOfFields[table.Name] = append(tableOfFields[table.Name], Field{
			Name:         getName(table.ColumnName),
			ColumnName:   table.ColumnName,
			Type:         dataType,
			ColumnType:   table.DataType,
			ColumnLength: table.DataLength,
			Comment:      table.ColumnComment,
			IsNull:       table.IsNull == "Yes",
			IsPrimaryKey: table.ColumnKey == "PRI",
			Default:      table.ColumnDefault,
		})
	}

	temp, err := template.New("").Parse(templateOfModel)
	if err != nil {
		panic(err)
	}

	dir := "models"
	dir = path.Join(dir, time.Now().Format(DateTimeLayout))
	if len(tableOfBaseModel) > 0 {
		go func() {
			if err := baseModel(dir); err != nil {
				fmt.Println("generate base_model err: ", err)
			}
			fmt.Println("generate base_model.go finish")
		}()
	}

	// 创建多级
	os.MkdirAll(dir, os.ModePerm)
	var wg sync.WaitGroup
	wg.Add(len(tableOfFields))
	for table, fields := range tableOfFields {
		go func(table string, fields []Field, wg *sync.WaitGroup) {
			defer wg.Done()

			modelName, ok := tableOfName[table]
			if !ok {
				modelName = table
			}

			packages := make([]string, 0)
			for k := range tableOfPackages[table] {
				packages = append(packages, k)
			}

			args := map[string]interface{}{
				"baseModel": "",
				"packages":  make([]string, 0),
				"table":     table,
				"model":     modelName,
				"fields":    fields,
			}
			if len(packages) > 0 {
				args["packages"] = packages
			}
			if _, ok := tableOfBaseModel[table]; ok {
				args["baseModel"] = "BaseModel"
			}

			var modelContentBytes bytes.Buffer
			err = temp.Execute(&modelContentBytes, args)
			if err != nil {
				fmt.Println("template err: ", err)
			}

			filePath := path.Join(dir, fmt.Sprintf("%s.go", table))
			if _, err = os.Stat(filePath); err != nil {
				_, _ = os.Create(filePath)
			}

			file, err := os.Open(filePath)
			if err != nil {
				return
			}
			defer file.Close()
			if err = os.WriteFile(filePath, modelContentBytes.Bytes(), 0644); err != nil {
				return
			}
		}(table, fields, &wg)
	}
	wg.Wait()
}

func getName(v string) string {
	vs := strings.Split(v, "_")
	nvs := make([]string, 0)
	for _, vv := range vs {
		nvs = append(nvs, strings.ToUpper(vv[:1])+vv[1:])
	}
	return strings.Join(nvs, "")
}

func getDataType(v string) string {
	if t, ok := mappingType[v]; ok {
		return t
	}

	return "string"
}

func baseModel(dir string) (err error) {
	baseModelTemplate, err := template.New("base_model").Parse(templateOfBaseModel)
	if err != nil {
		return
	}

	var contentBytes bytes.Buffer
	err = baseModelTemplate.Execute(&contentBytes, map[string]interface{}{
		"tag": "`gorm:\"column:id;primaryKey;autoIncrement;comment:主键编码\"`",
	})
	if err != nil {
		return
	}

	filePath := path.Join(dir, "base_model.go")
	if _, err = os.Stat(filePath); err != nil {
		_, _ = os.Create(filePath)
	}

	file, err := os.Open(filePath)
	if err != nil {
		return
	}
	defer file.Close()
	if err = os.WriteFile(filePath, contentBytes.Bytes(), 0644); err != nil {
		return
	}

	return
}
