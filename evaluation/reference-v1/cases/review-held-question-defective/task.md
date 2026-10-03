# review-held-question-defective task v1

Review the newly introduced DecodeAnswers API against its promise to reject unknown fields, unsupported schema versions and every trailing non-whitespace byte after the JSON document, while normalizing absent answers. Return verdict and distinct supported blockers with a trigger, impact, severity and source or reproduction evidence. Try closing-delimiter tails as well as a second JSON value. Scope is this decoder, not the entire historical PR.

Permitted source paths: `internal/question/question.go`, `internal/question/decode.go`.
Supplied instructions: ROLE.md and CONTEXT.md. All other history is excluded.
