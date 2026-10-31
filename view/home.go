package view

// LandingContent is the homepage body contract. Shared document and session
// details remain with the layout; components receive only their own data.
type LandingContent struct {
	Organization               Organization
	ChairEmail, PublicityEmail string
	Albums                     []Album
	Events                     []Event
	Announcements              []HomePost
	Posts                      []HomeForumPost
}

type HomeForumPost struct {
	HomePost
	Vote VoteControl
}

func (p Page) LandingContent() LandingContent {
	data := LandingContent{
		Organization: p.Organization, ChairEmail: p.ChairEmail, PublicityEmail: p.PublicityEmail,
		Albums: p.Media.CarouselAlbums(), Events: p.Home.Events, Announcements: p.Home.Announcements,
	}
	for _, post := range p.Home.Posts {
		data.Posts = append(data.Posts, HomeForumPost{
			HomePost: post,
			Vote:     voteControl(p, post.VoteURL, "/#home-forum", post.Title, post.Votes, post.UserVote),
		})
	}
	return data
}
