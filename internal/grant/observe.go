package grant

import "time"

// ObserveActive returns the active record, or nil if none is active, without
// creating a directory or lock file. It shares Active's strict record reader
// and selection rules, but not its lock: records can change between reads.
// This non-atomic observation must never replace a lifecycle authorization check.
func (s *Store) ObserveActive(now time.Time) (*Grant, error) {
	if s.empty() {
		return nil, nil
	}
	grants, err := s.all()
	if err != nil {
		return nil, err
	}
	return active(grants, now)
}
