package ghops

import "testing"

func TestMatchesPullRequestRepository(t *testing.T) {
	const prURL = "https://GitHub.Example.NET/Octo/Project.One/pull/43"
	for _, endpoint := range []string{
		"https://github.example.net/octo/project.one",
		"https://user:fixture-credential@github.example.net:443/OCTO/PROJECT.ONE.git",
		"ssh://git@github.example.net/Octo/Project.One.git",
		"ssh://git@github.example.net:22/Octo/Project.One",
		"git@GITHUB.EXAMPLE.NET:octo/project.one.git",
		"git@github.example.net:octo/project.one",
	} {
		if !MatchesPullRequestRepository(prURL, 43, endpoint) {
			t.Errorf("matching transport refused: %q", endpoint)
		}
	}
	for _, endpoint := range []string{
		"", "origin", "/tmp/octo/project.one.git", "file:///tmp/octo/project.one.git",
		"http://github.example.net/octo/project.one.git", "ext::git-upload-pack /tmp/repo",
		"https://other.example.net/octo/project.one.git", "https://github.example.net:444/octo/project.one.git",
		"https://github.example.net/other/project.one.git", "https://github.example.net/octo/other.git",
		"https://github.example.net/octo/project.one.git/", "https://github.example.net/octo/project.one.git?x=1",
		"https://github.example.net/octo/project.one.git#", "https://github.example.net/octo/%70roject.one.git",
		"https://github.example.net/octo%2fproject.one.git", "https://github.example.net/octo/../project.one.git",
		"https://github.example.net@other.example.net/octo/project.one.git",
		"ssh://other@github.example.net/octo/project.one.git", "ssh://git:pw@github.example.net/octo/project.one.git",
		"git@github.example.net:/octo/project.one.git", "git@github.example.net:22:octo/project.one.git",
	} {
		if MatchesPullRequestRepository(prURL, 43, endpoint) {
			t.Errorf("unverifiable transport accepted: %q", endpoint)
		}
	}
	for _, pr := range []string{
		"", "https://github.example.net/octo/project.one/issues/43", "https://github.example.net/octo/project.one/pull/44",
		"https://github.example.net/octo/project.one/pull/043", "https://github.example.net/octo/project.one/pull/43/",
		"https://github.example.net/octo/project.one/pull/43?", "https://user@github.example.net/octo/project.one/pull/43",
	} {
		if MatchesPullRequestRepository(pr, 43, "https://github.example.net/octo/project.one.git") {
			t.Errorf("unverifiable PR record accepted: %q", pr)
		}
	}
}
