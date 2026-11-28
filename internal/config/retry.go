package config

import (
	"errors"
	"strconv"

	"github.com/nickstrad/mini-gateway/internal/retry"
)

const DefaultRetryPolicy = "off"

type Retry struct {
	Policy    string `json:"policy"`
	BucketCap int64  `json:"bucket_cap"`
	Floor     int64  `json:"floor"`
}

func (f *File) applyRetryDefaults() {
	r := &f.Retry
	setString(&r.Policy, DefaultRetryPolicy)
	if r.BucketCap == 0 {
		r.BucketCap = retry.DefaultCapTokens
	}
	if r.Floor == 0 {
		r.Floor = retry.DefaultFloorTokens
		if r.BucketCap >= 1 && r.BucketCap <= retry.MaxTokens {
			r.Floor = min(r.Floor, r.BucketCap*int64(f.instances()))
		}
	}
}

func (f *File) instances() int {
	if f.Fleet.Instances < 1 {
		return DefaultFleetInstances
	}
	return f.Fleet.Instances
}

func (f *File) RetryFloorPerInstance() int64 {
	per, err := retry.Share(f.Retry.Floor, f.instances())
	if err != nil {
		return 1
	}
	return per
}

func (f *File) retryCore() retry.Config {
	kind, ok := retry.ParseKind(f.Retry.Policy)
	if !ok {
		kind = retry.KindOff
	}
	c := retry.Config{Kind: kind, CapTokens: f.Retry.BucketCap, FloorTokens: f.RetryFloorPerInstance()}
	if c.CapTokens < 1 || c.CapTokens > retry.MaxTokens {
		c.CapTokens = retry.DefaultCapTokens
	}
	c.FloorTokens = min(c.FloorTokens, c.CapTokens)
	return c
}

func (f *File) validateRetry() error {
	r := f.Retry
	var errs []error
	kind, ok := retry.ParseKind(r.Policy)
	if !ok {
		errs = append(errs, bad("retry.policy", "%q: must be off or pre-forward-once", r.Policy))
	}
	if r.BucketCap < 1 || r.BucketCap > retry.MaxTokens {
		errs = append(errs, bad("retry.bucket_cap", "must be 1 to %d tokens (0 or omitted means the default %d), got %d",
			retry.MaxTokens, retry.DefaultCapTokens, r.BucketCap))
	}
	if r.Floor < 1 || r.Floor > retry.MaxTokens {
		errs = append(errs, bad("retry.floor", "must be 1 to %d tokens (0 or omitted means the default %d), got %d",
			retry.MaxTokens, retry.DefaultFloorTokens, r.Floor))
	}
	if len(errs) > 0 || kind == retry.KindOff {
		return errors.Join(errs...)
	}
	per, err := retry.Share(r.Floor, f.instances())
	if err != nil {
		return bad("retry.floor", "%d over fleet.instances %d leaves less than 1 token per instance", r.Floor, f.instances())
	}
	if per > r.BucketCap {
		return bad("retry.floor", "%d per instance (%d over fleet.instances %d) is more than retry.bucket_cap %d", per, r.Floor,
			f.instances(), r.BucketCap)
	}
	return nil
}

func (f *File) retryWarnings() []string {
	n := int64(f.instances())
	if rem := f.Retry.Floor % n; rem != 0 && f.Retry.Policy != DefaultRetryPolicy {
		return []string{"retry.floor " + strconv.FormatInt(f.Retry.Floor, 10) + " leaves a remainder of " + strconv.FormatInt(rem, 10) +
			" over " + strconv.FormatInt(n, 10) + " instances; each takes " + strconv.FormatInt(f.Retry.Floor/n, 10)}
	}
	return nil
}
