package plugin

import (
	"time"

	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/gitremote"
)

func SetFetchBackoff(d time.Duration) (restore func()) {
	old := fetchBackoff
	fetchBackoff = d
	return func() { fetchBackoff = old }
}

// SetFetchMarker replaces the marker fetch, for the paths where ls-remote
// lists a marker that cannot then be read.
func SetFetchMarker(f func(dir, refspec string) error) (restore func()) {
	old := fetchMarker
	fetchMarker = func(_ gitremote.Remote, dir, refspec string) error { return f(dir, refspec) }
	return func() { fetchMarker = old }
}

// RealFetchMarker is the fetch SetFetchMarker replaced.
func RealFetchMarker(remote, dir, refspec string) error {
	return gitremote.Remote{URL: remote}.Fetch(dir, refspec)
}
