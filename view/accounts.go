package view

type ForgotPage struct {
	Email, EmailError string
	Submitted         bool
}

type InvitationPage struct {
	Value, Name, Email, Error, PasswordError string
	Done                                     bool
}

// LoginPage never retains a submitted password.
type LoginPage struct {
	Email, EmailError, PasswordError string
	Remember                         bool
}

// RegisterPage never retains a submitted password.
type RegisterPage struct {
	Name, Email, NameError, EmailError, PasswordError string
	ProgramID, ProgramError                           string
	Degree, DegreeError                               string
	Version, VersionError                             string
	Versions                                          []string
	Programs                                          []Program
	Submitted                                         bool
}

type TokenPage struct {
	Value, Kind, Error string
	Done               bool
}
