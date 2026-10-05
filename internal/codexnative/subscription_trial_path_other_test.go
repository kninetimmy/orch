//go:build !windows

package codexnative

import (
	"os"
	"reflect"
)

func trialSingleLink(file *os.File) bool {
	info, err := file.Stat()
	if err != nil {
		return false
	}
	value := reflect.Indirect(reflect.ValueOf(info.Sys()))
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return false
	}
	links := value.FieldByName("Nlink")
	return links.IsValid() && links.CanUint() && links.Uint() == 1
}
