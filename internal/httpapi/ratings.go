package httpapi

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/rating"
)

// ratingView is the public projection of a video's rating summary. MyRating is
// null for anonymous viewers or when the caller has not rated the video.
type ratingView struct {
	LikeCount    int64   `json:"like_count"`
	DislikeCount int64   `json:"dislike_count"`
	MyRating     *string `json:"my_rating"`
}

func newRatingView(s rating.Summary) ratingView {
	v := ratingView{LikeCount: s.Likes, DislikeCount: s.Dislikes}
	if s.Mine != "" {
		mine := s.Mine
		v.MyRating = &mine
	}
	return v
}

// handleGetVideoRating returns a public, published video's like/dislike counts,
// plus the caller's own rating when signed in. Behind optionalAuth.
func (s *Server) handleGetVideoRating(c echo.Context) error {
	videoID, err := s.publicVideoID(c)
	if err != nil {
		return err
	}
	userID, _, authed := principalFromContext(c)
	sum, err := s.ratingsvc.Get(c.Request().Context(), videoID, userID, authed)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, newRatingView(sum))
}

// setRatingRequest is the PUT /videos/{id}/rating body.
type setRatingRequest struct {
	Rating string `json:"rating"`
}

func (r setRatingRequest) Validate() []FieldError {
	if r.Rating != rating.Like && r.Rating != rating.Dislike {
		return []FieldError{{Field: "rating", Message: "must be 'like' or 'dislike'"}}
	}
	return nil
}

// handlePutVideoRating sets or changes the caller's rating for a video. Behind requireAuth.
func (s *Server) handlePutVideoRating(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	videoID, err := s.publicVideoID(c)
	if err != nil {
		return err
	}
	var in setRatingRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	sum, err := s.ratingsvc.Set(c.Request().Context(), videoID, userID, in.Rating)
	if err != nil {
		if errors.Is(err, rating.ErrInvalidRating) {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "invalid rating")
		}
		return err
	}
	return c.JSON(http.StatusOK, newRatingView(sum))
}

// handleDeleteVideoRating clears the caller's rating for a video (idempotent).
// Behind requireAuth.
func (s *Server) handleDeleteVideoRating(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	videoID, err := s.publicVideoID(c)
	if err != nil {
		return err
	}
	sum, err := s.ratingsvc.Clear(c.Request().Context(), videoID, userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, newRatingView(sum))
}
