package main
import (
  "fmt"
  hsqlite "github.com/FyrmForge/hamr/pkg/db/sqlite"
)
func main(){
  db,err:=hsqlite.Connect("file::memory:?cache=shared")
  fmt.Printf("dbnil=%v err=%v\n", db==nil, err)
}
