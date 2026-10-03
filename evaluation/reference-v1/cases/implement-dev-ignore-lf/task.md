# implement-dev-ignore-lf task v1

Pin the repository-root .gitignore to LF checkout line endings in .gitattributes. Preserve the existing LF rules for Go, shell and Markdown files. Use Git's native attributes; do not change .gitignore content or introduce dependencies. The external check will create its own disposable Git fixture with core.autocrlf=true and inspect checkout bytes.

Permitted source paths: `.gitattributes`, `.gitignore`.
Supplied instructions: ROLE.md and CONTEXT.md. All other history is excluded.
