package pool

import (
	_ "example.test/gw/internal/obs"
	_ "example.test/gw/internal/obs/csv"
	_ "example.test/gw/internal/pgwire"
	_ "github.com/jackc/pgx/v5/pgproto3"
	_ "sort"
)
