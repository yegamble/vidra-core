package usecase

import (
	uccmt "vidra-core/internal/usecase/comment"
)

type CommentService = uccmt.Service

var NewCommentService = uccmt.NewService
