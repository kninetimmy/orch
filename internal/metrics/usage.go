package metrics

import "encoding/json"

// legacyUsageWire preserves presence without changing the legacy Go request
// fields used by PROpen and Review. It is never treated as a native sample.
type legacyUsageWire struct {
	Counters
	DurationMS *int64 `json:"duration_ms,omitempty"`
}

// Counters exposes known legacy values. A constructed zero still means omitted,
// as it did in schema 1; only an explicitly decoded zero is measured zero.
func (u Usage) Counters() Counters {
	var fields [5]*int64
	for i, value := range [5]int64{u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheCreationTokens, u.TotalTokens} {
		if value != 0 || u.zeroFields&(1<<i) != 0 {
			fields[i] = &value
		}
	}
	return counters(fields)
}

func (u Usage) MarshalJSON() ([]byte, error) {
	wire := legacyUsageWire{Counters: u.Counters()}
	if u.DurationMS != 0 || u.zeroFields&(1<<5) != 0 {
		wire.DurationMS = &u.DurationMS
	}
	return json.Marshal(wire)
}

func (u *Usage) UnmarshalJSON(data []byte) error {
	var wire legacyUsageWire
	if err := strictDecode(data, &wire); err != nil {
		return err
	}
	*u = Usage{}
	values := []*int64{&u.InputTokens, &u.OutputTokens, &u.CacheReadTokens, &u.CacheCreationTokens, &u.TotalTokens, &u.DurationMS}
	fields := wire.Counters.fields()
	for i, v := range append(fields[:], wire.DurationMS) {
		if v != nil {
			*values[i] = *v
			if *v == 0 {
				u.zeroFields |= 1 << i
			}
		}
	}
	return nil
}
