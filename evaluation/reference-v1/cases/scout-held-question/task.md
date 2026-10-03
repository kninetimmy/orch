# scout-held-question task v1

Inspect DecodeAnswers and AnswerSet at this snapshot. Determine how unknown fields, unsupported schema versions and an omitted/null answers map are handled. Check whether the documented promise to reject all trailing data is actually met, including a second unmatched closing brace or bracket after a valid document. Give source or reproduction evidence and distinguish intent from behavior. Do not modify source.

Permitted source paths: `internal/question/question.go`, `internal/question/decode.go`.
Supplied instructions: ROLE.md and CONTEXT.md. All other history is excluded.
