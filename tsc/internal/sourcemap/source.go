package sourcemap

import "github.com/jdrebin/TyThon/tsc/internal/core"

type Source interface {
	Text() string
	FileName() string
	ECMALineMap() []core.TextPos
}
