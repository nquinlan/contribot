package contribot

// Submission contains the values gathered from a Contributor
type Submission struct {
	Name    string
	Address string
	Email   string
	Size    string
}

type backend func(*Submission)
