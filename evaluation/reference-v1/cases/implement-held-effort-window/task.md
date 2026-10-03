# implement-held-effort-window task v1

Update the effort-choice component in internal/interview/sequence.go and its shared use with configurelocal.go. Offer at most four contiguous enum values: the leading four unless the question's default occurs later, then the four ending at that default. Label options with their submitted enum tokens and mark exactly the offered default recommended. Preserve validEffort's full typed domain. Modify only this component and focused tests. Full interviews, model defaults and configuration are outside this case. The controller compiles exact selected declarations, not the unrelated interview machinery.

Permitted source paths: `internal/interview/sequence.go`, `internal/interview/configurelocal.go`, `internal/question/question.go`.
Supplied instructions: ROLE.md and CONTEXT.md. All other history is excluded.
