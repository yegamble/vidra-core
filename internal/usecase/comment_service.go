package usecase

import (
	uccmt "athena/internal/usecase/comment"
)

type CommentService = uccmt.Service

var NewCommentService = uccmt.NewService
