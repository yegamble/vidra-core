package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestSubscriptionJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	subID := uuid.New()
	subscriberID := uuid.New()
	channelID := uuid.New()

	sub := Subscription{
		ID:           subID,
		SubscriberID: subscriberID,
		ChannelID:    channelID,
		CreatedAt:    now,
	}

	data, err := json.Marshal(sub)
	assert.NoError(t, err)

	var decoded Subscription
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, subID, decoded.ID)
	assert.Equal(t, subscriberID, decoded.SubscriberID)
	assert.Equal(t, channelID, decoded.ChannelID)
	assert.Nil(t, decoded.Channel)
	assert.Nil(t, decoded.Subscriber)
}

func TestSubscriptionFieldsPresent(t *testing.T) {
	sub := Subscription{
		ID:           uuid.New(),
		SubscriberID: uuid.New(),
		ChannelID:    uuid.New(),
		CreatedAt:    time.Now(),
	}

	assert.NotEqual(t, uuid.Nil, sub.ID)
	assert.NotEqual(t, uuid.Nil, sub.SubscriberID)
	assert.NotEqual(t, uuid.Nil, sub.ChannelID)
	assert.False(t, sub.CreatedAt.IsZero())
}

func TestSubscriptionResponseJSON(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	resp := SubscriptionResponse{
		Total: 3,
		Data: []Subscription{
			{
				ID:           uuid.New(),
				SubscriberID: uuid.New(),
				ChannelID:    uuid.New(),
				CreatedAt:    now,
			},
			{
				ID:           uuid.New(),
				SubscriberID: uuid.New(),
				ChannelID:    uuid.New(),
				CreatedAt:    now,
			},
			{
				ID:           uuid.New(),
				SubscriberID: uuid.New(),
				ChannelID:    uuid.New(),
				CreatedAt:    now,
			},
		},
	}

	data, err := json.Marshal(resp)
	assert.NoError(t, err)

	var decoded SubscriptionResponse
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, 3, decoded.Total)
	assert.Len(t, decoded.Data, 3)

	for i, sub := range decoded.Data {
		assert.NotEqual(t, uuid.Nil, sub.ID, "Subscription %d should have non-nil ID", i)
		assert.NotEqual(t, uuid.Nil, sub.SubscriberID, "Subscription %d should have non-nil SubscriberID", i)
		assert.NotEqual(t, uuid.Nil, sub.ChannelID, "Subscription %d should have non-nil ChannelID", i)
	}
}

func TestSubscriptionResponseEmptyData(t *testing.T) {
	resp := SubscriptionResponse{
		Total: 0,
		Data:  []Subscription{},
	}

	data, err := json.Marshal(resp)
	assert.NoError(t, err)

	var decoded SubscriptionResponse
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, 0, decoded.Total)
	assert.Empty(t, decoded.Data)
}

func TestSubscriptionOmitsNestedObjects(t *testing.T) {
	sub := Subscription{
		ID:           uuid.New(),
		SubscriberID: uuid.New(),
		ChannelID:    uuid.New(),
		CreatedAt:    time.Now().Truncate(time.Second),
	}

	data, err := json.Marshal(sub)
	assert.NoError(t, err)

	var raw map[string]interface{}
	err = json.Unmarshal(data, &raw)
	assert.NoError(t, err)

	// Channel and Subscriber should be omitted when nil (omitempty)
	_, hasChannel := raw["channel"]
	_, hasSubscriber := raw["subscriber"]
	assert.False(t, hasChannel, "channel should be omitted when nil")
	assert.False(t, hasSubscriber, "subscriber should be omitted when nil")
}
