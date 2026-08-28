package lockfile

import "sync"

// Mutation holds the process-scoped lock that serializes one repository's
// mutating Delivery commands. Release must be deferred immediately after a
// successful AcquireMutation.
type Mutation struct {
	once    sync.Once
	release func() error
	err     error
}

// processMutation covers same-process callers because OS advisory locks may
// be process-owned rather than descriptor-owned on a supported platform.
// ponytail: this serializes different repositories only inside one process;
// key it by canonical root if Orch ever runs multiple repositories in-process.
var processMutation sync.Mutex

// AcquireMutation blocks until no other process is running a mutating
// Delivery command for repoRoot. The operating system owns the underlying
// lock, so process exit releases it without staleness detection or takeover.
func AcquireMutation(repoRoot string) (*Mutation, error) {
	processMutation.Lock()
	release, err := acquireMutationOS(repoRoot)
	if err != nil {
		processMutation.Unlock()
		return nil, err
	}
	return &Mutation{release: release}, nil
}

// Release gives the mutation serializer to the next waiter. It is idempotent.
func (m *Mutation) Release() error {
	if m == nil {
		return nil
	}
	m.once.Do(func() {
		m.err = m.release()
		processMutation.Unlock()
	})
	return m.err
}
