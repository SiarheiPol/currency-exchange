package queue

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// DedupKey computes the coalescing key for a producer:
//
//	sha256Hex(UPPER(base) + ":" + UPPER(quote) + ":" + bucket_unix_seconds)
//
// where bucket is now.Unix() floored to a multiple of bucketSize (the time
// grid is aligned to the Unix epoch). Returns a 64-character lower-case hex
// string.
func DedupKey(base, quote string, now time.Time, bucketSize time.Duration) string {
	bucketSecs := int64(bucketSize.Seconds())
	bucket := (now.Unix() / bucketSecs) * bucketSecs
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s:%s:%d", strings.ToUpper(base), strings.ToUpper(quote), bucket)
	return hex.EncodeToString(h.Sum(nil))
}
