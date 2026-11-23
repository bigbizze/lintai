package engine

import (
	"context"
	"github.com/bigbizze/lintai/observe"
)

func bundleUnchanged(set observe.Set) bool {
	changed, err := set.Changed(context.Background(), observe.Full)
	return err == nil && changed == nil
}
