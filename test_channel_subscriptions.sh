#!/bin/bash

# Test Channel Subscriptions

API_BASE="http://localhost:8080/api/v1"
EMAIL1="subscriber1@test.com"
PASSWORD="password123"
EMAIL2="subscriber2@test.com"

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo "Testing Channel Subscriptions..."

# 1. Register two users
echo "1. Registering users..."
curl -s -X POST "$API_BASE/../auth/register" \
  -H "Content-Type: application/json" \
  -d "{\"username\":\"subscriber1\",\"email\":\"$EMAIL1\",\"password\":\"$PASSWORD\"}" > /dev/null

USER1_TOKEN=$(curl -s -X POST "$API_BASE/../auth/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"$EMAIL1\",\"password\":\"$PASSWORD\"}" | jq -r '.data.access_token')

curl -s -X POST "$API_BASE/../auth/register" \
  -H "Content-Type: application/json" \
  -d "{\"username\":\"subscriber2\",\"email\":\"$EMAIL2\",\"password\":\"$PASSWORD\"}" > /dev/null

USER2_TOKEN=$(curl -s -X POST "$API_BASE/../auth/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"$EMAIL2\",\"password\":\"$PASSWORD\"}" | jq -r '.data.access_token')

echo -e "${GREEN}✓ Users registered and logged in${NC}"

# 2. User1 creates a channel
echo "2. Creating channel for user1..."
CHANNEL1=$(curl -s -X POST "$API_BASE/channels" \
  -H "Authorization: Bearer $USER1_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "handle": "user1channel",
    "displayName": "User 1 Channel",
    "description": "Test channel for user 1"
  }')

CHANNEL1_ID=$(echo $CHANNEL1 | jq -r '.data.id')
echo -e "${GREEN}✓ Channel created: $CHANNEL1_ID${NC}"

# 3. User2 creates a channel
echo "3. Creating channel for user2..."
CHANNEL2=$(curl -s -X POST "$API_BASE/channels" \
  -H "Authorization: Bearer $USER2_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "handle": "user2channel",
    "displayName": "User 2 Channel",
    "description": "Test channel for user 2"
  }')

CHANNEL2_ID=$(echo $CHANNEL2 | jq -r '.data.id')
echo -e "${GREEN}✓ Channel created: $CHANNEL2_ID${NC}"

# 4. User2 subscribes to User1's channel
echo "4. User2 subscribing to User1's channel..."
SUBSCRIBE_RESULT=$(curl -s -X POST "$API_BASE/channels/$CHANNEL1_ID/subscribe" \
  -H "Authorization: Bearer $USER2_TOKEN")

if echo "$SUBSCRIBE_RESULT" | jq -e '.message' > /dev/null; then
  echo -e "${GREEN}✓ Successfully subscribed${NC}"
else
  echo -e "${RED}✗ Subscribe failed: $SUBSCRIBE_RESULT${NC}"
fi

# 5. Check if subscription exists
echo "5. Checking subscription status..."
IS_SUBSCRIBED=$(curl -s -X GET "$API_BASE/channels/$CHANNEL1_ID" \
  -H "Authorization: Bearer $USER2_TOKEN" | jq -r '.subscriberCount // 0')

if [ "$IS_SUBSCRIBED" != "0" ]; then
  echo -e "${GREEN}✓ Subscription confirmed${NC}"
else
  echo -e "${RED}✗ Subscription not reflected${NC}"
fi

# 6. Get channel subscribers
echo "6. Getting channel subscribers..."
SUBSCRIBERS=$(curl -s -X GET "$API_BASE/channels/$CHANNEL1_ID/subscribers" \
  -H "Authorization: Bearer $USER2_TOKEN")

SUBSCRIBER_COUNT=$(echo $SUBSCRIBERS | jq -r '.total')
if [ "$SUBSCRIBER_COUNT" = "1" ]; then
  echo -e "${GREEN}✓ Found 1 subscriber${NC}"
else
  echo -e "${RED}✗ Expected 1 subscriber, got $SUBSCRIBER_COUNT${NC}"
fi

# 7. Get user's subscriptions
echo "7. Getting user2's subscriptions..."
MY_SUBS=$(curl -s -X GET "$API_BASE/users/me/subscriptions" \
  -H "Authorization: Bearer $USER2_TOKEN")

SUB_COUNT=$(echo $MY_SUBS | jq -r '.total')
if [ "$SUB_COUNT" = "1" ]; then
  echo -e "${GREEN}✓ User has 1 subscription${NC}"
else
  echo -e "${RED}✗ Expected 1 subscription, got $SUB_COUNT${NC}"
fi

# 8. Try to subscribe to own channel (should fail)
echo "8. Testing self-subscription (should fail)..."
SELF_SUB=$(curl -s -X POST "$API_BASE/channels/$CHANNEL2_ID/subscribe" \
  -H "Authorization: Bearer $USER2_TOKEN")

if echo "$SELF_SUB" | jq -r '.error' | grep -q "Cannot subscribe to your own channel"; then
  echo -e "${GREEN}✓ Self-subscription correctly blocked${NC}"
else
  echo -e "${RED}✗ Self-subscription not blocked properly${NC}"
fi

# 9. Unsubscribe from channel
echo "9. Unsubscribing from channel..."
UNSUB_RESULT=$(curl -s -X DELETE "$API_BASE/channels/$CHANNEL1_ID/subscribe" \
  -H "Authorization: Bearer $USER2_TOKEN")

if echo "$UNSUB_RESULT" | jq -e '.message' > /dev/null; then
  echo -e "${GREEN}✓ Successfully unsubscribed${NC}"
else
  echo -e "${RED}✗ Unsubscribe failed${NC}"
fi

# 10. Verify unsubscription
echo "10. Verifying unsubscription..."
MY_SUBS_AFTER=$(curl -s -X GET "$API_BASE/users/me/subscriptions" \
  -H "Authorization: Bearer $USER2_TOKEN")

SUB_COUNT_AFTER=$(echo $MY_SUBS_AFTER | jq -r '.total')
if [ "$SUB_COUNT_AFTER" = "0" ]; then
  echo -e "${GREEN}✓ Unsubscription confirmed${NC}"
else
  echo -e "${RED}✗ Still showing subscriptions after unsubscribe${NC}"
fi

echo ""
echo "Channel subscription tests completed!"
