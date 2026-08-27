package question

import (
	"fmt"
	"slices"
	"testing"
)

func TestPaginateOptionsIsHostValidAndComplete(t *testing.T) {
	for _, count := range []int{1, 7} {
		t.Run(fmt.Sprintf("%d-models", count), func(t *testing.T) {
			options := make([]Option, count)
			want := make([]string, count)
			for i := range options {
				want[i] = fmt.Sprintf("provider/model-%d", i+1)
				options[i] = Option{Value: want[i], Label: want[i], Recommended: i == 0}
			}
			pagination := PaginateOptions(options)
			for _, host := range []string{"claude", "codex", "opencode"} {
				if !slices.Contains(pagination.Hosts, host) {
					t.Errorf("hosts = %v, missing %s", pagination.Hosts, host)
				}
			}

			var got []string
			for i, page := range pagination.Pages {
				if page.Index != i+1 || page.Total != len(pagination.Pages) {
					t.Errorf("page %d position = %d/%d, want %d/%d", i, page.Index, page.Total, i+1, len(pagination.Pages))
				}
				if len(page.Options) < 2 || len(page.Options) > 3 {
					t.Errorf("page %d has %d options, want 2-3", i+1, len(page.Options))
				}
				seen := map[string]bool{}
				for _, option := range page.Options {
					if (option.Value == "") == (option.Action == "") {
						t.Errorf("page %d option %+v must carry exactly one of value/action", i+1, option)
					}
					identity := "value:" + option.Value
					if option.Action != "" {
						identity = "action:" + option.Action
					}
					if seen[identity] {
						t.Errorf("page %d repeats mutually exclusive choice %q", i+1, identity)
					}
					seen[identity] = true
					if option.Value != "" {
						got = append(got, option.Value)
					}
				}
			}
			if !slices.Equal(got, want) {
				t.Errorf("selectable answers = %v, want each model exactly once in order %v", got, want)
			}
			q := Question{ID: "model", Header: "Model", Prompt: "Choose", Kind: KindSelect, Options: options, Pagination: pagination, Default: want[0]}
			if err := SpecCheck(q); err != nil {
				t.Errorf("SpecCheck: %v", err)
			}
		})
	}
}

func TestPaginateOptionsLayouts(t *testing.T) {
	one := PaginateOptions([]Option{{Value: "only", Label: "Only"}})
	if got := one.Pages[0].Options; len(got) != 2 || got[0].Value != "only" || got[1].Action != PageCancel {
		t.Errorf("one-model page = %+v", got)
	}

	four := PaginateOptions([]Option{{Value: "a"}, {Value: "b"}, {Value: "c"}, {Value: "d"}})
	if len(four.Pages) != 2 || four.Pages[0].Options[2].Action != PageNext || four.Pages[1].Options[0].Action != PagePrevious {
		t.Errorf("four-model pages = %+v", four.Pages)
	}
}

func TestSpecCheckRejectsTamperedPagination(t *testing.T) {
	options := []Option{{Value: "a", Label: "A"}, {Value: "b", Label: "B"}, {Value: "c", Label: "C"}, {Value: "d", Label: "D"}}
	pagination := PaginateOptions(options)
	pagination.Pages[0].Options[0], pagination.Pages[0].Options[1] = pagination.Pages[0].Options[1], pagination.Pages[0].Options[0]
	q := Question{ID: "model", Header: "Model", Prompt: "Choose", Kind: KindSelect, Options: options, Pagination: pagination}
	if err := SpecCheck(q); err == nil {
		t.Fatal("SpecCheck accepted reordered pagination")
	}
}
