#!/bin/bash

# Simple subscription test

# Register user1
echo "1. Register user1..."
USER1=$(curl -s -X POST "http://localhost:8080/auth/register" \
  -H "Content-Type: application/json" \
  -d '{"username":"user1test","email":"user1test@test.com","password":"password123"}')
TOKEN1=$(echo "$USER1" | jq -r '.data.access_token')
echo "User1 token: ${TOKEN1:0:20}..."

# Register user2
echo "2. Register user2..."
USER2=$(curl -s -X POST "http://localhost:8080/auth/register" \
  -H "Content-Type: application/json" \
  -d '{"username":"user2test","email":"user2test@test.com","password":"password123"}')
TOKEN2=$(echo "$USER2" | jq -r '.data.access_token')
echo "User2 token: ${TOKEN2:0:20}..."

# User1 creates channel
echo "3. User1 creates channel..."
CHANNEL1=$(curl -s -X POST "http://localhost:8080/api/v1/channels" \
  -H "Authorization: Bearer $TOKEN1" \
  -H "Content-Type: application/json" \
  -d '{"handle":"user1testchannel","displayName":"User1 Test Channel","description":"Test"}')
CHANNEL1_ID=$(echo "$CHANNEL1" | jq -r '.data.id')
echo "Channel1 ID: $CHANNEL1_ID"

# User2 subscribes to User1's channel
echo "4. User2 subscribes to User1's channel..."
SUB_RESULT=$(curl -s -X POST "http://localhost:8080/api/v1/channels/$CHANNEL1_ID/subscribe" \
  -H "Authorization: Bearer $TOKEN2")
echo "Subscribe result: $SUB_RESULT"

# Check subscription
echo "5. Check User2's subscriptions..."
MY_SUBS=$(curl -s -X GET "http://localhost:8080/api/v1/users/me/subscriptions" \
  -H "Authorization: Bearer $TOKEN2")
echo "My subscriptions: $MY_SUBS"

# Get channel subscribers
echo "6. Get channel subscribers..."
SUBSCRIBERS=$(curl -s -X GET "http://localhost:8080/api/v1/channels/$CHANNEL1_ID/subscribers")
echo "Channel subscribers: $SUBSCRIBERS"
