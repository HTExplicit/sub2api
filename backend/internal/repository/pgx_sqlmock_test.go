package repository

import (
	"database/sql/driver"
	"reflect"
)

// pgxSQLMockValueConverter mirrors pgx/stdlib's native slice parameter support.
// go-sqlmock otherwise rejects PostgreSQL array parameters before expectations run.
type pgxSQLMockValueConverter struct{}

func (pgxSQLMockValueConverter) ConvertValue(value any) (driver.Value, error) {
	typeOfValue := reflect.TypeOf(value)
	if typeOfValue != nil && typeOfValue.Kind() == reflect.Slice && typeOfValue.Elem().Kind() != reflect.Uint8 {
		return value, nil
	}
	return driver.DefaultParameterConverter.ConvertValue(value)
}
