package view

type FeedbackPage struct {
	Path, Value, Expected, Actual, Error string
	Sent                                 bool
}
