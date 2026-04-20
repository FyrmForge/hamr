package main
import (
  "embed"
  "fmt"
  "os"
  "path/filepath"
  hsqlite "github.com/FyrmForge/hamr/pkg/db/sqlite"
)
//go:embed tmpmig/*.sql
var fsys embed.FS
func main(){
  path:=filepath.Join(os.TempDir(),"hmr-begin.db")
  db,err:=hsqlite.Connect(path)
  if err!=nil { panic(err) }
  defer db.Close()
  err=hsqlite.Migrate(db,hsqlite.MigrateConfig{FS:fsys,Directory:"tmpmig"})
  fmt.Printf("err=%v\n",err)
}
