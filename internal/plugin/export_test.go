package plugin

import "time"

func SetFetchBackoff(d time.Duration) (restore func()) {
	old := fetchBackoff
	fetchBackoff = d
	return func() { fetchBackoff = old }
}
