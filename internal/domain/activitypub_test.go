package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestActivityPubContextConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{"ActivityStreamsContext", ActivityStreamsContext, "https://www.w3.org/ns/activitystreams"},
		{"SecurityContext", SecurityContext, "https://w3id.org/security/v1"},
		{"PeerTubeContext", PeerTubeContext, "https://joinpeertube.org/ns"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.constant)
		})
	}
}

func TestActivityTypeConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{"Create", ActivityTypeCreate, "Create"},
		{"Update", ActivityTypeUpdate, "Update"},
		{"Delete", ActivityTypeDelete, "Delete"},
		{"Follow", ActivityTypeFollow, "Follow"},
		{"Accept", ActivityTypeAccept, "Accept"},
		{"Reject", ActivityTypeReject, "Reject"},
		{"Add", ActivityTypeAdd, "Add"},
		{"Remove", ActivityTypeRemove, "Remove"},
		{"Like", ActivityTypeLike, "Like"},
		{"Undo", ActivityTypeUndo, "Undo"},
		{"Announce", ActivityTypeAnnounce, "Announce"},
		{"View", ActivityTypeView, "View"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.constant)
		})
	}
}

func TestObjectTypeConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{"Person", ObjectTypePerson, "Person"},
		{"Group", ObjectTypeGroup, "Group"},
		{"Video", ObjectTypeVideo, "Video"},
		{"Note", ObjectTypeNote, "Note"},
		{"Image", ObjectTypeImage, "Image"},
		{"Document", ObjectTypeDocument, "Document"},
		{"OrderedCollection", ObjectTypeOrderedCollection, "OrderedCollection"},
		{"OrderedCollectionPage", ObjectTypeOrderedCollectionPage, "OrderedCollectionPage"},
		{"Collection", ObjectTypeCollection, "Collection"},
		{"CollectionPage", ObjectTypeCollectionPage, "CollectionPage"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.constant)
		})
	}
}

func TestActorJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	actor := Actor{
		Context:           ActivityStreamsContext,
		Type:              ObjectTypePerson,
		ID:                "https://example.com/users/alice",
		Following:         "https://example.com/users/alice/following",
		Followers:         "https://example.com/users/alice/followers",
		Inbox:             "https://example.com/users/alice/inbox",
		Outbox:            "https://example.com/users/alice/outbox",
		PreferredUsername: "alice",
		Name:              "Alice",
		Summary:           "A test user",
		URL:               "https://example.com/@alice",
		PublicKey: &PublicKey{
			ID:           "https://example.com/users/alice#main-key",
			Owner:        "https://example.com/users/alice",
			PublicKeyPem: "-----BEGIN PUBLIC KEY-----\nMIIBIjAN...\n-----END PUBLIC KEY-----",
		},
		Published: &now,
		Endpoints: &Endpoints{
			SharedInbox: "https://example.com/inbox",
		},
	}

	data, err := json.Marshal(actor)
	assert.NoError(t, err)
	assert.NotEmpty(t, data)

	var decoded Actor
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, actor.Type, decoded.Type)
	assert.Equal(t, actor.ID, decoded.ID)
	assert.Equal(t, actor.Inbox, decoded.Inbox)
	assert.Equal(t, actor.Outbox, decoded.Outbox)
	assert.Equal(t, actor.PreferredUsername, decoded.PreferredUsername)
	assert.Equal(t, actor.Name, decoded.Name)
	assert.Equal(t, actor.Summary, decoded.Summary)
	assert.Equal(t, actor.URL, decoded.URL)
	assert.NotNil(t, decoded.PublicKey)
	assert.Equal(t, actor.PublicKey.ID, decoded.PublicKey.ID)
	assert.Equal(t, actor.PublicKey.Owner, decoded.PublicKey.Owner)
	assert.Equal(t, actor.PublicKey.PublicKeyPem, decoded.PublicKey.PublicKeyPem)
	assert.NotNil(t, decoded.Published)
	assert.Equal(t, now.UTC(), decoded.Published.UTC())
	assert.NotNil(t, decoded.Endpoints)
	assert.Equal(t, actor.Endpoints.SharedInbox, decoded.Endpoints.SharedInbox)
}

func TestActorJSONMinimalFields(t *testing.T) {
	actor := Actor{
		Type:              ObjectTypePerson,
		ID:                "https://example.com/users/bob",
		Inbox:             "https://example.com/users/bob/inbox",
		Outbox:            "https://example.com/users/bob/outbox",
		PreferredUsername: "bob",
	}

	data, err := json.Marshal(actor)
	assert.NoError(t, err)

	var decoded Actor
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, actor.Type, decoded.Type)
	assert.Equal(t, actor.ID, decoded.ID)
	assert.Equal(t, actor.Inbox, decoded.Inbox)
	assert.Equal(t, actor.Outbox, decoded.Outbox)
	assert.Equal(t, actor.PreferredUsername, decoded.PreferredUsername)
	assert.Nil(t, decoded.PublicKey)
	assert.Nil(t, decoded.Icon)
	assert.Nil(t, decoded.Image)
	assert.Nil(t, decoded.Published)
	assert.Nil(t, decoded.Endpoints)
}

func TestWebFingerResponseJSONRoundTrip(t *testing.T) {
	response := WebFingerResponse{
		Subject: "acct:alice@example.com",
		Aliases: []string{"https://example.com/@alice", "https://example.com/users/alice"},
		Links: []WebFingerLink{
			{
				Rel:  "self",
				Type: "application/activity+json",
				Href: "https://example.com/users/alice",
			},
			{
				Rel:  "http://webfinger.net/rel/profile-page",
				Type: "text/html",
				Href: "https://example.com/@alice",
			},
		},
	}

	data, err := json.Marshal(response)
	assert.NoError(t, err)

	var decoded WebFingerResponse
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, response.Subject, decoded.Subject)
	assert.Equal(t, response.Aliases, decoded.Aliases)
	assert.Len(t, decoded.Links, 2)
	assert.Equal(t, response.Links[0].Rel, decoded.Links[0].Rel)
	assert.Equal(t, response.Links[0].Type, decoded.Links[0].Type)
	assert.Equal(t, response.Links[0].Href, decoded.Links[0].Href)
	assert.Equal(t, response.Links[1].Rel, decoded.Links[1].Rel)
}

func TestNodeInfoJSONRoundTrip(t *testing.T) {
	nodeInfo := NodeInfo{
		Version: "2.0",
		Software: NodeInfoSoftware{
			Name:       "vidra",
			Version:    "1.0.0",
			Repository: "https://github.com/example/vidra",
			Homepage:   "https://example.com",
		},
		Protocols: []string{"activitypub"},
		Services: NodeInfoServices{
			Inbound:  []string{},
			Outbound: []string{},
		},
		OpenRegistrations: true,
		Usage: NodeInfoUsage{
			Users: NodeInfoUsers{
				Total:          100,
				ActiveHalfyear: 80,
				ActiveMonth:    50,
			},
			LocalPosts:    500,
			LocalComments: 1200,
		},
		Metadata: map[string]interface{}{
			"nodeName": "Test Instance",
		},
	}

	data, err := json.Marshal(nodeInfo)
	assert.NoError(t, err)

	var decoded NodeInfo
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, nodeInfo.Version, decoded.Version)
	assert.Equal(t, nodeInfo.Software.Name, decoded.Software.Name)
	assert.Equal(t, nodeInfo.Software.Version, decoded.Software.Version)
	assert.Equal(t, nodeInfo.Software.Repository, decoded.Software.Repository)
	assert.Equal(t, nodeInfo.Protocols, decoded.Protocols)
	assert.Equal(t, nodeInfo.OpenRegistrations, decoded.OpenRegistrations)
	assert.Equal(t, nodeInfo.Usage.Users.Total, decoded.Usage.Users.Total)
	assert.Equal(t, nodeInfo.Usage.Users.ActiveHalfyear, decoded.Usage.Users.ActiveHalfyear)
	assert.Equal(t, nodeInfo.Usage.Users.ActiveMonth, decoded.Usage.Users.ActiveMonth)
	assert.Equal(t, nodeInfo.Usage.LocalPosts, decoded.Usage.LocalPosts)
	assert.Equal(t, nodeInfo.Usage.LocalComments, decoded.Usage.LocalComments)
	assert.Equal(t, "Test Instance", decoded.Metadata["nodeName"])
}

func TestActivityJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	activity := Activity{
		Context:   ActivityStreamsContext,
		Type:      ActivityTypeCreate,
		ID:        "https://example.com/activities/1",
		Actor:     "https://example.com/users/alice",
		Object:    "https://example.com/videos/1",
		Published: &now,
		To:        []string{"https://www.w3.org/ns/activitystreams#Public"},
		Cc:        []string{"https://example.com/users/alice/followers"},
	}

	data, err := json.Marshal(activity)
	assert.NoError(t, err)

	var decoded Activity
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, activity.Type, decoded.Type)
	assert.Equal(t, activity.ID, decoded.ID)
	assert.Equal(t, activity.Actor, decoded.Actor)
	assert.NotNil(t, decoded.Published)
	assert.Equal(t, activity.To, decoded.To)
	assert.Equal(t, activity.Cc, decoded.Cc)
}

func TestOrderedCollectionJSONRoundTrip(t *testing.T) {
	collection := OrderedCollection{
		Context:    ActivityStreamsContext,
		Type:       ObjectTypeOrderedCollection,
		ID:         "https://example.com/users/alice/outbox",
		TotalItems: 42,
		First:      "https://example.com/users/alice/outbox?page=1",
		Last:       "https://example.com/users/alice/outbox?page=5",
	}

	data, err := json.Marshal(collection)
	assert.NoError(t, err)

	var decoded OrderedCollection
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, collection.Type, decoded.Type)
	assert.Equal(t, collection.ID, decoded.ID)
	assert.Equal(t, collection.TotalItems, decoded.TotalItems)
	assert.Equal(t, collection.First, decoded.First)
	assert.Equal(t, collection.Last, decoded.Last)
}

func TestVideoObjectJSONRoundTrip(t *testing.T) {
	videoObj := VideoObject{
		Type:            ObjectTypeVideo,
		ID:              "https://example.com/videos/1",
		Name:            "Test Video",
		Duration:        "PT5M30S",
		UUID:            "test-uuid-123",
		Views:           1000,
		Sensitive:       false,
		CommentsEnabled: true,
		DownloadEnabled: true,
		AttributedTo:    []string{"https://example.com/users/alice"},
		Tag: []APTag{
			{Type: "Hashtag", Name: "#test", Href: "https://example.com/tags/test"},
		},
	}

	data, err := json.Marshal(videoObj)
	assert.NoError(t, err)

	var decoded VideoObject
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, videoObj.Type, decoded.Type)
	assert.Equal(t, videoObj.ID, decoded.ID)
	assert.Equal(t, videoObj.Name, decoded.Name)
	assert.Equal(t, videoObj.Duration, decoded.Duration)
	assert.Equal(t, videoObj.UUID, decoded.UUID)
	assert.Equal(t, videoObj.Views, decoded.Views)
	assert.Equal(t, videoObj.CommentsEnabled, decoded.CommentsEnabled)
	assert.Equal(t, videoObj.DownloadEnabled, decoded.DownloadEnabled)
	assert.Equal(t, videoObj.AttributedTo, decoded.AttributedTo)
	assert.Len(t, decoded.Tag, 1)
	assert.Equal(t, "#test", decoded.Tag[0].Name)
}
