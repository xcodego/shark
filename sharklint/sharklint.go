package sharklint

import (
	"errors"

	"github.com/xcodego/shark/sharklint/go/any"
	"github.com/xcodego/shark/sharklint/go/beego"
	"github.com/xcodego/shark/sharklint/go/format"
	"github.com/xcodego/shark/sharklint/go/goroutine"
	"github.com/xcodego/shark/sharklint/go/logging"
	"github.com/xcodego/shark/sharklint/go/maprace"
	sharklint_panic "github.com/xcodego/shark/sharklint/go/panic"
	"github.com/xcodego/shark/sharklint/go/syncpair"
	"github.com/xcodego/shark/sharklint/go/unreachable"
	"github.com/xcodego/shark/sharklint/go/vet"
	sharklint_redis "github.com/xcodego/shark/sharklint/redis"
	sharklint_sql "github.com/xcodego/shark/sharklint/sql"
	sharklint_swagger "github.com/xcodego/shark/sharklint/swagger"
)

func Lint(dir string) error {
	return errors.Join(
		any.Lint(dir),
		beego.Lint(dir),
		format.Lint(dir),
		goroutine.Lint(dir),
		logging.Lint(dir),
		maprace.Lint(dir),
		sharklint_panic.Lint(dir),
		syncpair.Lint(dir),
		sharklint_redis.Lint(dir),
		sharklint_sql.Lint(dir),
		sharklint_swagger.Lint(dir),
		unreachable.Lint(dir),
		vet.Lint(dir),
	)
}
