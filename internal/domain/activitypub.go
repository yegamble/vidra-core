package domain

import (
	"encoding/json"
	"time"
)

const (
	ActivityStreamsContext = "https://www.w3.org/ns/activitystreams"
	SecurityContext        = "https://w3id.org/security/v1"
	PeerTubeContext        = "https://joinpeertube.org/ns"
)

const (
	ActivityTypeCreate   = "Create"
	ActivityTypeUpdate   = "Update"
	ActivityTypeDelete   = "Delete"
	ActivityTypeFollow   = "Follow"
	ActivityTypeAccept   = "Accept"
	ActivityTypeReject   = "Reject"
	ActivityTypeAdd      = "Add"
	ActivityTypeRemove   = "Remove"
	ActivityTypeLike     = "Like"
	ActivityTypeUndo     = "Undo"
	ActivityTypeAnnounce = "Announce"
	ActivityTypeView     = "View"
)

const (
	ObjectTypePerson                = "Person"
	ObjectTypeGroup                 = "Group"
	ObjectTypeVideo                 = "Video"
	ObjectTypeNote                  = "Note"
	ObjectTypeImage                 = "Image"
	ObjectTypeDocument              = "Document"
	ObjectTypeOrderedCollection     = "OrderedCollection"
	ObjectTypeOrderedCollectionPage = "OrderedCollectionPage"
	ObjectTypeCollection            = "Collection"
	ObjectTypeCollectionPage        = "CollectionPage"
)

type Actor struct {
	Context                   interface{} `json:"@context,omitempty"`
	Type                      string      `json:"type"`
	ID                        string      `json:"id"`
	Following                 string      `json:"following,omitempty"`
	Followers                 string      `json:"followers,omitempty"`
	Inbox                     string      `json:"inbox"`
	Outbox                    string      `json:"outbox"`
	PreferredUsername         string      `json:"preferredUsername"`
	Name                      string      `json:"name,omitempty"`
	Summary                   string      `json:"summary,omitempty"`
	URL                       string      `json:"url,omitempty"`
	ManuallyApprovesFollowers bool        `json:"manuallyApprovesFollowers,omitempty"`
	PublicKey                 *PublicKey  `json:"publicKey,omitempty"`
	Icon                      *Image      `json:"icon,omitempty"`
	Image                     *Image      `json:"image,omitempty"`
	Published                 *time.Time  `json:"published,omitempty"`
	Endpoints                 *Endpoints  `json:"endpoints,omitempty"`
}

type PublicKey struct {
	ID           string `json:"id"`
	Owner        string `json:"owner"`
	PublicKeyPem string `json:"publicKeyPem"`
}

type Endpoints struct {
	SharedInbox string `json:"sharedInbox,omitempty"`
}

type Image struct {
	Type      string `json:"type"`
	MediaType string `json:"mediaType,omitempty"`
	URL       string `json:"url"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
}

type Activity struct {
	Context   interface{} `json:"@context,omitempty"`
	Type      string      `json:"type"`
	ID        string      `json:"id,omitempty"`
	Actor     string      `json:"actor"`
	Object    interface{} `json:"object"`
	Target    interface{} `json:"target,omitempty"`
	Published *time.Time  `json:"published,omitempty"`
	To        []string    `json:"to,omitempty"`
	Cc        []string    `json:"cc,omitempty"`
	BTo       []string    `json:"bto,omitempty"`
	BCc       []string    `json:"bcc,omitempty"`
}

type VideoObject struct {
	Context         interface{}    `json:"@context,omitempty"`
	Type            string         `json:"type"`
	ID              string         `json:"id"`
	Name            string         `json:"name,omitempty"`
	Duration        string         `json:"duration,omitempty"`
	UUID            string         `json:"uuid,omitempty"`
	Category        *APCategory    `json:"category,omitempty"`
	Licence         *APLicence     `json:"licence,omitempty"`
	Language        *APLanguage    `json:"language,omitempty"`
	Views           int            `json:"views,omitempty"`
	Sensitive       bool           `json:"sensitive,omitempty"`
	WaitTranscoding bool           `json:"waitTranscoding,omitempty"`
	State           int            `json:"state,omitempty"`
	CommentsEnabled bool           `json:"commentsEnabled,omitempty"`
	DownloadEnabled bool           `json:"downloadEnabled,omitempty"`
	Published       *time.Time     `json:"published,omitempty"`
	Updated         *time.Time     `json:"updated,omitempty"`
	MediaType       string         `json:"mediaType,omitempty"`
	Content         string         `json:"content,omitempty"`
	Summary         string         `json:"summary,omitempty"`
	Support         string         `json:"support,omitempty"`
	Icon            []Image        `json:"icon,omitempty"`
	URL             []APUrl        `json:"url,omitempty"`
	Likes           string         `json:"likes,omitempty"`
	Dislikes        string         `json:"dislikes,omitempty"`
	Shares          string         `json:"shares,omitempty"`
	Comments        string         `json:"comments,omitempty"`
	AttributedTo    []string       `json:"attributedTo,omitempty"`
	To              []string       `json:"to,omitempty"`
	Cc              []string       `json:"cc,omitempty"`
	Tag             []APTag        `json:"tag,omitempty"`
	Attachment      []APAttachment `json:"attachment,omitempty"`
}

// NoteObject represents a comment/note in ActivityPub format
type NoteObject struct {
	Context      interface{} `json:"@context,omitempty"`
	Type         string      `json:"type"`
	ID           string      `json:"id"`
	Content      string      `json:"content"`
	Published    *time.Time  `json:"published,omitempty"`
	Updated      *time.Time  `json:"updated,omitempty"`
	AttributedTo string      `json:"attributedTo"`
	InReplyTo    string      `json:"inReplyTo,omitempty"`
	To           []string    `json:"to,omitempty"`
	Cc           []string    `json:"cc,omitempty"`
	Tag          []APTag     `json:"tag,omitempty"`
}

type APUrl struct {
	Type      string `json:"type"`
	MediaType string `json:"mediaType"`
	Href      string `json:"href"`
	Height    int    `json:"height,omitempty"`
	Width     int    `json:"width,omitempty"`
	Size      int64  `json:"size,omitempty"`
	FPS       int    `json:"fps,omitempty"`
}

type APTag struct {
	Type string `json:"type"`
	Name string `json:"name"`
	Href string `json:"href,omitempty"`
}

type APCategory struct {
	Identifier string `json:"identifier"`
	Name       string `json:"name,omitempty"`
}

type APLicence struct {
	Identifier string `json:"identifier"`
	Name       string `json:"name,omitempty"`
}

type APLanguage struct {
	Identifier string `json:"identifier"`
	Name       string `json:"name,omitempty"`
}

type APAttachment struct {
	Type      string `json:"type"`
	MediaType string `json:"mediaType"`
	Href      string `json:"href"`
	Name      string `json:"name,omitempty"`
}

type OrderedCollection struct {
	Context      interface{} `json:"@context,omitempty"`
	Type         string      `json:"type"`
	ID           string      `json:"id"`
	TotalItems   int         `json:"totalItems"`
	First        string      `json:"first,omitempty"`
	Last         string      `json:"last,omitempty"`
	OrderedItems interface{} `json:"orderedItems,omitempty"`
}

type OrderedCollectionPage struct {
	Context      interface{} `json:"@context,omitempty"`
	Type         string      `json:"type"`
	ID           string      `json:"id"`
	TotalItems   int         `json:"totalItems,omitempty"`
	PartOf       string      `json:"partOf,omitempty"`
	Next         string      `json:"next,omitempty"`
	Prev         string      `json:"prev,omitempty"`
	OrderedItems interface{} `json:"orderedItems"`
}

type APFollower struct {
	ID         string    `json:"id" db:"id"`
	ActorID    string    `json:"actor_id" db:"actor_id"`
	FollowerID string    `json:"follower_id" db:"follower_id"`
	State      string    `json:"state" db:"state"`
	CreatedAt  time.Time `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time `json:"updated_at" db:"updated_at"`
}

type APActivity struct {
	ID           string          `json:"id" db:"id"`
	ActivityURI  string          `json:"activity_uri" db:"activity_uri"`
	ActorID      string          `json:"actor_id" db:"actor_id"`
	Type         string          `json:"type" db:"type"`
	ObjectID     *string         `json:"object_id,omitempty" db:"object_id"`
	ObjectType   *string         `json:"object_type,omitempty" db:"object_type"`
	TargetID     *string         `json:"target_id,omitempty" db:"target_id"`
	Published    time.Time       `json:"published" db:"published"`
	ActivityJSON json.RawMessage `json:"activity_json" db:"activity_json"`
	Local        bool            `json:"local" db:"local"`
	CreatedAt    time.Time       `json:"created_at" db:"created_at"`
}

type APRemoteActor struct {
	ID            string     `json:"id" db:"id"`
	ActorURI      string     `json:"actor_uri" db:"actor_uri"`
	Type          string     `json:"type" db:"type"`
	Username      string     `json:"username" db:"username"`
	Domain        string     `json:"domain" db:"domain"`
	DisplayName   *string    `json:"display_name,omitempty" db:"display_name"`
	Summary       *string    `json:"summary,omitempty" db:"summary"`
	InboxURL      string     `json:"inbox_url" db:"inbox_url"`
	OutboxURL     *string    `json:"outbox_url,omitempty" db:"outbox_url"`
	SharedInbox   *string    `json:"shared_inbox,omitempty" db:"shared_inbox"`
	FollowersURL  *string    `json:"followers_url,omitempty" db:"followers_url"`
	FollowingURL  *string    `json:"following_url,omitempty" db:"following_url"`
	PublicKeyID   string     `json:"public_key_id" db:"public_key_id"`
	PublicKeyPem  string     `json:"public_key_pem" db:"public_key_pem"`
	IconURL       *string    `json:"icon_url,omitempty" db:"icon_url"`
	ImageURL      *string    `json:"image_url,omitempty" db:"image_url"`
	LastFetchedAt *time.Time `json:"last_fetched_at,omitempty" db:"last_fetched_at"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at" db:"updated_at"`
}

type APDeliveryQueue struct {
	ID          string    `json:"id" db:"id"`
	ActivityID  string    `json:"activity_id" db:"activity_id"`
	InboxURL    string    `json:"inbox_url" db:"inbox_url"`
	ActorID     string    `json:"actor_id" db:"actor_id"`
	Attempts    int       `json:"attempts" db:"attempts"`
	MaxAttempts int       `json:"max_attempts" db:"max_attempts"`
	NextAttempt time.Time `json:"next_attempt" db:"next_attempt"`
	LastError   *string   `json:"last_error,omitempty" db:"last_error"`
	Status      string    `json:"status" db:"status"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

type WebFingerLink struct {
	Rel  string `json:"rel"`
	Type string `json:"type,omitempty"`
	Href string `json:"href"`
}

type WebFingerResponse struct {
	Subject string          `json:"subject"`
	Aliases []string        `json:"aliases,omitempty"`
	Links   []WebFingerLink `json:"links"`
}

type NodeInfo struct {
	Version           string                 `json:"version"`
	Software          NodeInfoSoftware       `json:"software"`
	Protocols         []string               `json:"protocols"`
	Services          NodeInfoServices       `json:"services"`
	OpenRegistrations bool                   `json:"openRegistrations"`
	Usage             NodeInfoUsage          `json:"usage"`
	Metadata          map[string]interface{} `json:"metadata,omitempty"`
}

type NodeInfoSoftware struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	Repository string `json:"repository,omitempty"`
	Homepage   string `json:"homepage,omitempty"`
}

type NodeInfoServices struct {
	Inbound  []string `json:"inbound"`
	Outbound []string `json:"outbound"`
}

type NodeInfoUsage struct {
	Users         NodeInfoUsers `json:"users"`
	LocalPosts    int           `json:"localPosts,omitempty"`
	LocalComments int           `json:"localComments,omitempty"`
}

type NodeInfoUsers struct {
	Total          int `json:"total,omitempty"`
	ActiveHalfyear int `json:"activeHalfyear,omitempty"`
	ActiveMonth    int `json:"activeMonth,omitempty"`
}
