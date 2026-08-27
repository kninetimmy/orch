package question

const (
	PageNext     = "next"
	PagePrevious = "previous"
	PageCancel   = "cancel"
)

// Pagination is the shared setup-host presentation contract for a select.
// Hosts lists every adapter that can present Pages unchanged.
type Pagination struct {
	Hosts []string `json:"hosts"`
	Pages []Page   `json:"pages"`
}

// Page is one native question call. Options always contains two or three
// mutually exclusive choices.
type Page struct {
	Index   int          `json:"index"`
	Total   int          `json:"total"`
	Options []PageOption `json:"options"`
}

// PageOption is either an answer Value copied from Question.Options or a
// navigation Action handled by the adapter. Exactly one is non-empty.
type PageOption struct {
	Value       string `json:"value,omitempty"`
	Action      string `json:"action,omitempty"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
}

// PaginateOptions returns deterministic pages valid for Claude Code, Codex,
// and OpenCode setup dialogs. A one-answer question gains a cancel choice;
// larger domains use explicit previous/next navigation while each answer
// appears on exactly one page.
func PaginateOptions(options []Option) *Pagination {
	if len(options) == 0 {
		return nil
	}
	pages := make([]Page, 0, len(options))
	if len(options) <= 3 {
		page := answerPage(options)
		if len(page) == 1 {
			page = append(page, pageAction(PageCancel, "Cancel setup"))
		}
		pages = append(pages, Page{Options: page})
	} else {
		pages = append(pages, Page{Options: append(answerPage(options[:2]), pageAction(PageNext, "Next choices"))})
		remaining := options[2:]
		for len(remaining) > 2 {
			pages = append(pages, Page{Options: []PageOption{
				pageAction(PagePrevious, "Previous choices"), answerPageOption(remaining[0]), pageAction(PageNext, "Next choices"),
			}})
			remaining = remaining[1:]
		}
		last := []PageOption{pageAction(PagePrevious, "Previous choices")}
		last = append(last, answerPage(remaining)...)
		pages = append(pages, Page{Options: last})
	}
	for i := range pages {
		pages[i].Index = i + 1
		pages[i].Total = len(pages)
	}
	return &Pagination{Hosts: []string{"claude", "codex", "opencode"}, Pages: pages}
}

func answerPage(options []Option) []PageOption {
	page := make([]PageOption, len(options))
	for i, option := range options {
		page[i] = answerPageOption(option)
	}
	return page
}

func answerPageOption(option Option) PageOption {
	return PageOption{
		Value: option.Value, Label: option.Label, Description: option.Description, Recommended: option.Recommended,
	}
}

func pageAction(action, label string) PageOption {
	return PageOption{Action: action, Label: label}
}
