package usecase

import ucmsg "athena/internal/usecase/message"

// Backwards-compatible aliases while we migrate to feature slice packages
type MessageService = ucmsg.Service

var NewMessageService = ucmsg.NewService
