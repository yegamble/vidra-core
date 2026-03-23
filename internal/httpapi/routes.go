package httpapi

import (
	"athena/internal/httpapi/handlers/account"
	"athena/internal/httpapi/handlers/admin"
	"athena/internal/httpapi/handlers/auth"
	"athena/internal/httpapi/handlers/autotags"
	backuphandlers "athena/internal/httpapi/handlers/backup"
	"athena/internal/httpapi/handlers/channel"
	compat "athena/internal/httpapi/handlers/compat"
	clientconfig "athena/internal/httpapi/handlers/config"
	"athena/internal/httpapi/handlers/federation"
	"athena/internal/httpapi/handlers/livestream"
	"athena/internal/httpapi/handlers/messaging"
	metricshandlers "athena/internal/httpapi/handlers/metrics"
	migrationhandlers "athena/internal/httpapi/handlers/migration"
	"athena/internal/httpapi/handlers/misc"
	"athena/internal/httpapi/handlers/moderation"
	"athena/internal/httpapi/handlers/payments"
	"athena/internal/httpapi/handlers/player"
	pluginhandlers "athena/internal/httpapi/handlers/plugin"
	runnerhandlers "athena/internal/httpapi/handlers/runner"
	"athena/internal/httpapi/handlers/social"
	statichandlers "athena/internal/httpapi/handlers/static"
	userhandlers "athena/internal/httpapi/handlers/user"
	"athena/internal/httpapi/handlers/video"
	"athena/internal/httpapi/handlers/watchedwords"
	"athena/internal/httpapi/shared"
	"athena/internal/repository"
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	chi "github.com/go-chi/chi/v5"
	govalidator "github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/middleware"
	importuc "athena/internal/usecase/import"
)

func RegisterRoutesWithDependencies(r chi.Router, cfg *config.Config, rlManager *middleware.RateLimiterManager, deps *shared.HandlerDependencies) { //nolint:gocyclo
	generalBurst := cfg.RateLimitRequests
	strictAuthBurst := 5
	strictLoginBurst := 10
	strictImportBurst := 10

	// Keep production limits unchanged, but avoid cross-collection throttling in validation/E2E runs.
	if cfg.ValidationTestMode {
		generalBurst = 10000
		strictAuthBurst = 1000
		strictLoginBurst = 1000
		strictImportBurst = 1000
	}

	generalLimiter := rlManager.CreateRateLimiter(cfg.RateLimitDuration, generalBurst)
	r.Use(generalLimiter.Limit)

	defaultMaxRequestBytes, err := middleware.ParseByteSize(cfg.APIMaxRequestSize)
	if err != nil {
		defaultMaxRequestBytes = 10 * 1024 * 1024
		log.Printf("Invalid API_MAX_REQUEST_SIZE value %q; using default %d bytes: %v", cfg.APIMaxRequestSize, defaultMaxRequestBytes, err)
	}

	uploadMaxRequestBytes := cfg.MaxUploadSize
	if uploadMaxRequestBytes <= 0 {
		uploadMaxRequestBytes = defaultMaxRequestBytes
	}

	r.Use(middleware.SizeLimiterWithOverrides(defaultMaxRequestBytes, []middleware.RequestSizeOverride{
		{PathPrefix: "/api/v1/uploads", MaxBytes: uploadMaxRequestBytes},
		{PathPrefix: "/api/v1/videos/", PathSuffix: "/upload", MaxBytes: uploadMaxRequestBytes},
		{PathPrefix: "/api/v1/users/me/avatar", MaxBytes: uploadMaxRequestBytes},
	}))

	strictAuthLimiter := rlManager.CreateRateLimiter(60*time.Second, strictAuthBurst)
	strictLoginLimiter := rlManager.CreateRateLimiter(60*time.Second, strictLoginBurst)
	strictImportLimiter := rlManager.CreateRateLimiter(60*time.Second, strictImportBurst)

	server := NewServerWithOAuth(
		deps.UserRepo,
		deps.SessionRepo,
		deps.OAuthRepo,
		deps.JWTSecret,
		deps.Redis,
		deps.RedisPingTimeout,
		deps.IPFSApi,
		deps.IPFSCluster,
		deps.IPFSPingTimeout,
		cfg,
	)

	if deps.TwoFAService != nil {
		server.SetTwoFAService(deps.TwoFAService)
	}
	if deps.DB != nil {
		server.SetDB(deps.DB)
	}

	authHandlers := auth.NewAuthHandlers(
		deps.UserRepo,
		deps.SessionRepo,
		deps.OAuthRepo,
		deps.EmailVerificationService,
		deps.JWTSecret,
		deps.Redis,
		deps.RedisPingTimeout,
		deps.IPFSApi,
		deps.IPFSCluster,
		cfg,
	)

	r.With(strictAuthLimiter.Limit).Post("/auth/register", server.Register)
	r.With(strictLoginLimiter.Limit).Post("/auth/login", server.Login)
	r.Post("/auth/refresh", server.RefreshToken)
	r.With(middleware.Auth(cfg.JWTSecret)).Post("/auth/logout", server.Logout)

	r.Route("/auth/2fa", func(r chi.Router) {
		r.Use(middleware.Auth(cfg.JWTSecret))
		twoFAHandlers := auth.NewTwoFAHandlers(deps.TwoFAService)
		r.Post("/setup", twoFAHandlers.SetupTwoFA)
		r.Post("/verify-setup", twoFAHandlers.VerifyTwoFASetup)
		r.Post("/disable", twoFAHandlers.DisableTwoFA)
		r.Post("/regenerate-backup-codes", twoFAHandlers.RegenerateBackupCodes)
		r.Get("/status", twoFAHandlers.GetTwoFAStatus)
	})

	if deps.EmailVerificationService != nil {
		emailVerificationHandlers := auth.NewEmailVerificationHandlers(deps.EmailVerificationService)
		r.Post("/auth/email/verify", emailVerificationHandlers.VerifyEmail)
		r.Post("/auth/email/resend", emailVerificationHandlers.ResendVerification)
	}

	if deps.PasswordResetRepo != nil && deps.EmailService != nil {
		passwordResetHandlers := auth.NewPasswordResetHandlers(deps.PasswordResetRepo, deps.UserRepo, deps.EmailService)
		r.With(strictAuthLimiter.Limit).Post("/users/ask-reset-password", passwordResetHandlers.AskResetPassword)
		r.Post("/users/{id}/reset-password", passwordResetHandlers.ResetPassword)
	}

	r.Post("/oauth/token", authHandlers.OAuthToken)
	r.HandleFunc("/oauth/authorize", authHandlers.OAuthAuthorize)
	r.Post("/oauth/revoke", authHandlers.OAuthRevoke)
	r.Post("/oauth/introspect", authHandlers.OAuthIntrospect)

	r.Get("/health", server.HealthCheck)
	r.Get("/ready", server.ReadinessCheck)

	// RSS/Atom feed endpoints (PeerTube compatible, outside /api/v1)
	feedHandlers := video.NewFeedHandlers(deps.VideoRepo, deps.CommentRepo, cfg.PublicBaseURL)
	if deps.SubRepo != nil {
		feedHandlers.SetSubRepo(deps.SubRepo)
	}
	r.Get("/feeds/videos.atom", feedHandlers.VideosFeed)
	r.Get("/feeds/videos.rss", feedHandlers.VideosFeedRSS)
	r.Get("/feeds/video-comments.atom", feedHandlers.CommentsFeed)
	r.Get("/feeds/video-comments.rss", feedHandlers.CommentsFeed)
	r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/feeds/subscriptions.atom", feedHandlers.SubscriptionFeedAtom)
	r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/feeds/subscriptions.rss", feedHandlers.SubscriptionFeedRSS)
	r.Get("/feeds/podcast/videos.xml", feedHandlers.PodcastFeed)

	if cfg.EnableActivityPub && deps.ActivityPubService != nil {
		apHandlers := federation.NewActivityPubHandlers(deps.ActivityPubService, cfg, deps.UserRepo, deps.VideoRepo)

		r.Get("/.well-known/webfinger", apHandlers.WebFinger)
		r.Get("/.well-known/nodeinfo", apHandlers.NodeInfo)
		r.Get("/.well-known/host-meta", apHandlers.HostMeta)
		r.Get("/nodeinfo/2.0", apHandlers.NodeInfo20)

		r.Post("/inbox", apHandlers.PostSharedInbox)

		r.Route("/users/{username}", func(r chi.Router) {
			r.Get("/", apHandlers.GetActor)
			r.Get("/outbox", apHandlers.GetOutbox)
			r.Get("/inbox", apHandlers.GetInbox)
			r.Post("/inbox", apHandlers.PostInbox)
			r.Get("/followers", apHandlers.GetFollowers)
			r.Get("/following", apHandlers.GetFollowing)
		})
	}

	r.Route("/api/v1", func(r chi.Router) {
		viewsHandler := video.NewViewsHandler(deps.ViewsService)

		// Avatar proxy — unauthenticated, avatars are content-addressed and public
		r.Get("/avatars/{cid}", authHandlers.ServeAvatarFromIPFS)

		r.Route("/videos", func(r chi.Router) {
			log.Printf("Registering video routes...")
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/", video.ListVideosHandler(deps.VideoRepo))
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/search", video.SearchVideosHandler(deps.VideoRepo))
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/qualities", video.GetSupportedQualities)
			r.Get("/licences", video.GetVideoLicences)
			r.Get("/languages", video.GetVideoLanguages)
			r.Get("/privacies", video.GetVideoPrivacies)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/top", viewsHandler.GetTopVideos)
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/upload", video.UploadVideoFileHandler(deps.VideoRepo, cfg))

			// PeerTube-compatible resumable upload alias
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/upload-resumable", video.InitiateUploadHandler(deps.UploadService, deps.VideoRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/upload-resumable", compat.PeerTubeNotImplemented("Resumable upload chunk via PUT"))
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/upload-resumable", compat.PeerTubeNotImplemented("Resumable upload cancel"))

			// PeerTube-compatible category alias: GET /videos/categories → /categories
			if deps.VideoCategoryUseCase != nil {
				catHandler := video.NewVideoCategoryHandler(deps.VideoCategoryUseCase)
				r.Get("/categories", catHandler.ListCategories)
			}

			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}", video.GetVideoHandler(deps.VideoRepo, deps.CaptionService))
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}/stream", video.StreamVideoHandler(deps.VideoRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/{id}/source", video.GetVideoSourceHandler(deps.VideoRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/subscriptions", channel.ListSubscriptionVideosHandler(deps.SubRepo))

			r.With(middleware.Auth(cfg.JWTSecret)).Post("/", video.CreateVideoHandler(deps.VideoRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/{id}", video.UpdateVideoHandler(deps.VideoRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{id}", video.DeleteVideoHandler(deps.VideoRepo))
			if deps.OwnershipRepo != nil {
				r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/give-ownership", video.GiveOwnershipHandler(deps.OwnershipRepo, deps.VideoRepo))
			}
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{id}/source", video.DeleteVideoSourceHandler(deps.VideoRepo))
			if deps.Redis != nil {
				tokenStore := repository.NewRedisVideoTokenStore(deps.Redis)
				r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/token", video.CreateVideoTokenHandler(deps.VideoRepo, tokenStore))
			}

			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/upload", video.VideoUploadChunkHandler(deps.UploadService, cfg))
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/complete", video.VideoCompleteUploadHandler(deps.UploadService))

			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Post("/{id}/views", viewsHandler.TrackView)
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/{id}/analytics", viewsHandler.GetVideoAnalytics)
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/{id}/stats/daily", viewsHandler.GetDailyStats)
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/{id}/stats/overall", video.GetVideoStatsOverallHandler())
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/{id}/stats/retention", video.GetVideoStatsRetentionHandler())

			commentHandlers := social.NewCommentHandlers(deps.CommentService)
			r.Route("/{videoId}/comments", func(r chi.Router) {
				r.Get("/", commentHandlers.GetComments)
				r.With(middleware.Auth(cfg.JWTSecret)).Post("/", commentHandlers.CreateComment)
			})

			ratingHandlers := social.NewRatingHandlers(deps.RatingService)
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/{id}/rating", ratingHandlers.SetRating)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}/rating", ratingHandlers.GetRating)
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{id}/rating", ratingHandlers.RemoveRating)

			playlistHandlers := social.NewPlaylistHandlers(deps.PlaylistService)
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/watch-later", playlistHandlers.AddToWatchLater)

			r.With(middleware.Auth(cfg.JWTSecret)).Get("/{id}/encoding-jobs", video.GetEncodingJobsByVideoHandler(deps.EncodingRepo, deps.VideoRepo))

			captionHandlers := social.NewCaptionHandlers(deps.CaptionService, deps.VideoRepo)
			r.Route("/{id}/captions", func(r chi.Router) {
				r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/", captionHandlers.GetCaptions)
				r.With(middleware.Auth(cfg.JWTSecret)).Post("/", captionHandlers.CreateCaption)
				r.Route("/{captionId}", func(r chi.Router) {
					r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/content", captionHandlers.GetCaptionContent)
					r.With(middleware.Auth(cfg.JWTSecret)).Put("/", captionHandlers.UpdateCaption)
					r.With(middleware.Auth(cfg.JWTSecret)).Delete("/", captionHandlers.DeleteCaption)
				})

				if deps.CaptionGenService != nil {
					captionGenHandlers := social.NewCaptionGenerationHandlers(deps.CaptionGenService, deps.VideoRepo)
					r.With(middleware.Auth(cfg.JWTSecret)).Post("/generate", captionGenHandlers.GenerateCaptions)
					r.With(middleware.Auth(cfg.JWTSecret)).Get("/jobs", captionGenHandlers.ListCaptionGenerationJobs)
				}
			})
		})

		r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/hls/*", video.HLSHandler(deps.VideoRepo))

		if deps.ImportService != nil {
			log.Printf("Registering video import routes...")
			importService, ok := deps.ImportService.(importuc.Service)
			if ok {
				importHandlers := video.NewImportHandlers(importService)
				r.Route("/videos/imports", func(r chi.Router) {
					r.Use(middleware.Auth(cfg.JWTSecret))
					r.With(strictImportLimiter.Limit).Post("/", importHandlers.CreateImport)
					r.Get("/", importHandlers.ListImports)
					r.Get("/{id}", importHandlers.GetImport)
					r.Post("/{id}/cancel", importHandlers.CancelImportCanonical)
					r.Post("/{id}/retry", importHandlers.RetryImport)
					r.Delete("/{id}", importHandlers.CancelImport)
				})
				// PeerTube alias: /users/me/videos/imports → same handler
				r.With(middleware.Auth(cfg.JWTSecret)).Get("/users/me/videos/imports", importHandlers.ListImports)
			}
		}

		r.Route("/uploads", func(r chi.Router) {
			r.Use(middleware.Auth(cfg.JWTSecret))
			r.Post("/initiate", video.InitiateUploadHandler(deps.UploadService, deps.VideoRepo))
			r.Route("/{sessionId}", func(r chi.Router) {
				r.Post("/chunks", video.UploadChunkHandler(deps.UploadService, cfg))
				r.Post("/complete", video.CompleteUploadHandler(deps.UploadService, deps.EncodingRepo))
				r.Get("/status", video.GetUploadStatusHandler(deps.UploadService))
				r.Get("/resume", video.ResumeUploadHandler(deps.UploadService))
			})
		})

		r.Route("/encoding", func(r chi.Router) {
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/status", video.EncodingStatusHandlerEnhanced(deps.EncodingRepo, cfg, deps.EncodingScheduler))

			r.Route("/jobs", func(r chi.Router) {
				r.Use(middleware.Auth(cfg.JWTSecret))
				r.Get("/{jobID}", video.GetEncodingJobHandler(deps.EncodingRepo, deps.VideoRepo))
			})

			r.With(middleware.Auth(cfg.JWTSecret)).Get("/my-jobs", video.GetMyEncodingJobsHandler(deps.EncodingRepo, deps.VideoRepo))
		})

		jobHandlers := admin.NewJobHandlers(deps.EncodingRepo, deps.EncodingScheduler)
		r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole(string(domain.RoleAdmin), string(domain.RoleMod))).Post("/jobs/pause", jobHandlers.PauseJobs)
		r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole(string(domain.RoleAdmin), string(domain.RoleMod))).Post("/jobs/resume", jobHandlers.ResumeJobs)
		r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole(string(domain.RoleAdmin), string(domain.RoleMod))).Get("/jobs", jobHandlers.ListJobs)
		r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole(string(domain.RoleAdmin), string(domain.RoleMod))).Get("/jobs/{state}", jobHandlers.ListJobs)

		r.Route("/users", func(r chi.Router) {
			r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole("admin")).Post("/", auth.CreateUserHandler(deps.UserRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me", auth.GetCurrentUserHandler(deps.UserRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/me", auth.UpdateCurrentUserHandler(deps.UserRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/me", auth.DeleteAccountHandler(deps.UserRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/me/avatar", authHandlers.UploadAvatar)
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/me/avatar", authHandlers.DeleteAvatar)
			if deps.RegistrationRepo != nil {
				regHandlers := admin.NewRegistrationHandlers(deps.RegistrationRepo, deps.UserRepo)
				r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole(string(domain.RoleAdmin), string(domain.RoleMod))).Get("/registrations", regHandlers.ListRegistrations)
				r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole(string(domain.RoleAdmin), string(domain.RoleMod))).Post("/registrations/{registrationId}/accept", regHandlers.AcceptRegistration)
				r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole(string(domain.RoleAdmin), string(domain.RoleMod))).Post("/registrations/{registrationId}/reject", regHandlers.RejectRegistration)
				r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole(string(domain.RoleAdmin), string(domain.RoleMod))).Delete("/registrations/{registrationId}", regHandlers.DeleteRegistration)
			} else {
				registrationsNotImplemented := compat.PeerTubeNotImplemented("PeerTube user registrations moderation")
				r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole(string(domain.RoleAdmin), string(domain.RoleMod))).Get("/registrations", registrationsNotImplemented)
				r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole(string(domain.RoleAdmin), string(domain.RoleMod))).Post("/registrations/{registrationId}/accept", registrationsNotImplemented)
				r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole(string(domain.RoleAdmin), string(domain.RoleMod))).Post("/registrations/{registrationId}/reject", registrationsNotImplemented)
				r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole(string(domain.RoleAdmin), string(domain.RoleMod))).Delete("/registrations/{registrationId}", registrationsNotImplemented)
			}
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}", auth.GetPublicUserHandler(deps.UserRepo))
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}/videos", video.GetUserVideosHandler(deps.VideoRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/subscribe", channel.SubscribeToUserHandler(deps.SubRepo, deps.UserRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{id}/subscribe", channel.UnsubscribeFromUserHandler(deps.SubRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me/subscriptions", channel.ListMySubscriptionsHandler(deps.SubRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me/subscriptions/exist", channel.CheckSubscriptionsExistHandler(deps.SubRepo, deps.ChannelService))
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/me/subscriptions", channel.SubscribeByHandleHandler(deps.SubRepo, deps.ChannelService))
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me/subscriptions/{subscriptionHandle}", channel.GetSubscriptionByHandleHandler(deps.SubRepo, deps.ChannelService))
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/me/subscriptions/{subscriptionHandle}", channel.UnsubscribeByHandleHandler(deps.SubRepo, deps.ChannelService))

			channelHandlers := channel.NewChannelHandlers(deps.ChannelService, deps.SubRepo)
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me/channels", channelHandlers.GetMyChannels)

			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me/videos", video.GetMyVideosHandler(deps.VideoRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me/comments", video.GetMyCommentsHandler())
			if deps.OwnershipRepo != nil {
				r.With(middleware.Auth(cfg.JWTSecret)).Get("/me/videos/ownership", video.ListOwnershipChangesHandler(deps.OwnershipRepo))
				r.With(middleware.Auth(cfg.JWTSecret)).Post("/me/videos/ownership/{id}/accept", video.AcceptOwnershipHandler(deps.OwnershipRepo, deps.VideoRepo))
				r.With(middleware.Auth(cfg.JWTSecret)).Post("/me/videos/ownership/{id}/refuse", video.RefuseOwnershipHandler(deps.OwnershipRepo))
			}

			ratingHandlers := social.NewRatingHandlers(deps.RatingService)
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me/ratings", ratingHandlers.GetUserRatings)

			playlistHandlers := social.NewPlaylistHandlers(deps.PlaylistService)
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me/watch-later", playlistHandlers.GetWatchLater)

			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me/notification-preferences", auth.GetNotificationPreferencesHandler(deps.NotificationPrefRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/me/notification-preferences", auth.UpdateNotificationPreferencesHandler(deps.NotificationPrefRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me/video-quota-used", auth.GetVideoQuotaUsedHandler(deps.VideoRepo))

			if deps.TokenSessionRepo != nil {
				tokenSessionHandlers := auth.NewTokenSessionHandlers(deps.TokenSessionRepo)
				r.With(middleware.Auth(cfg.JWTSecret)).Get("/{id}/token-sessions", tokenSessionHandlers.ListTokenSessions)
				r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/token-sessions/{tokenSessionId}/revoke", tokenSessionHandlers.RevokeTokenSession)
			}

			// User data import/export (archive) routes
			if archiveRepo, ok := deps.ArchiveRepo.(userhandlers.ArchiveRepository); ok {
				archiveHandlers := userhandlers.NewArchiveHandlers(archiveRepo)
				r.Route("/{userId}/exports", func(r chi.Router) {
					r.Use(middleware.Auth(cfg.JWTSecret))
					r.Post("/request", archiveHandlers.RequestExport)
					r.Get("/", archiveHandlers.ListExports)
					r.Delete("/{id}", archiveHandlers.DeleteExport)
				})
				r.Route("/{userId}/imports", func(r chi.Router) {
					r.Use(middleware.Auth(cfg.JWTSecret))
					r.Post("/import-resumable", archiveHandlers.InitImportResumable)
					r.Put("/import-resumable", archiveHandlers.UploadImportChunk)
					r.Delete("/import-resumable", archiveHandlers.CancelImportResumable)
					r.Get("/latest", archiveHandlers.GetLatestImport)
				})
			}
		})

		// PeerTube-compatible /video-channels/{channelHandle} routes
		r.Route("/video-channels", func(r chi.Router) {
			channelHandlers := channel.NewChannelHandlers(deps.ChannelService, deps.SubRepo)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{channelHandle}", channelHandlers.GetChannelByHandleParam)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{channelHandle}/videos", channelHandlers.GetChannelVideosByHandleParam)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{channelHandle}/video-playlists", social.GetChannelPlaylistsHandler(deps.ChannelService, deps.PlaylistService))

			if deps.CollaboratorRepo != nil {
				collaboratorHandlers := channel.NewCollaboratorHandlers(deps.ChannelRepo, deps.UserRepo, deps.CollaboratorRepo)
				r.With(middleware.Auth(cfg.JWTSecret)).Get("/{channelHandle}/collaborators", collaboratorHandlers.ListCollaborators)
				r.With(middleware.Auth(cfg.JWTSecret)).Post("/{channelHandle}/collaborators/invite", collaboratorHandlers.InviteCollaborator)
				r.With(middleware.Auth(cfg.JWTSecret)).Post("/{channelHandle}/collaborators/{collaboratorId}/accept", collaboratorHandlers.AcceptCollaborator)
				r.With(middleware.Auth(cfg.JWTSecret)).Post("/{channelHandle}/collaborators/{collaboratorId}/reject", collaboratorHandlers.RejectCollaborator)
				r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{channelHandle}/collaborators/{collaboratorId}", collaboratorHandlers.DeleteCollaborator)
			} else {
				collaboratorsNotImplemented := compat.PeerTubeNotImplemented("PeerTube channel collaborators")
				r.With(middleware.Auth(cfg.JWTSecret)).Get("/{channelHandle}/collaborators", collaboratorsNotImplemented)
				r.With(middleware.Auth(cfg.JWTSecret)).Post("/{channelHandle}/collaborators/invite", collaboratorsNotImplemented)
				r.With(middleware.Auth(cfg.JWTSecret)).Post("/{channelHandle}/collaborators/{collaboratorId}/accept", collaboratorsNotImplemented)
				r.With(middleware.Auth(cfg.JWTSecret)).Post("/{channelHandle}/collaborators/{collaboratorId}/reject", collaboratorsNotImplemented)
				r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{channelHandle}/collaborators/{collaboratorId}", collaboratorsNotImplemented)
			}
		})

		// Video channel syncs
		if syncRepo, ok := deps.ChannelSyncRepo.(channel.ChannelSyncRepository); ok {
			syncHandlers := channel.NewSyncHandlers(syncRepo)
			r.Route("/video-channel-syncs", func(r chi.Router) {
				r.Use(middleware.Auth(cfg.JWTSecret))
				r.Post("/", syncHandlers.CreateSync)
				r.Delete("/{id}", syncHandlers.DeleteSync)
				r.Post("/{id}/trigger-now", syncHandlers.TriggerSync)
			})
		}

		// Player settings
		if psRepo, ok := deps.PlayerSettingsRepo.(player.PlayerSettingsRepository); ok {
			playerHandlers := player.NewSettingsHandlers(psRepo)
			r.Route("/player-settings", func(r chi.Router) {
				r.Get("/videos/{videoId}", playerHandlers.GetVideoSettings)
				r.With(middleware.Auth(cfg.JWTSecret)).Put("/videos/{videoId}", playerHandlers.UpdateVideoSettings)
				r.Get("/video-channels/{handle}", playerHandlers.GetChannelSettings)
				r.With(middleware.Auth(cfg.JWTSecret)).Put("/video-channels/{handle}", playerHandlers.UpdateChannelSettings)
			})
		}

		// Client configuration
		clientConfigHandlers := clientconfig.NewClientConfigHandlers()
		r.Post("/client-config/update-interface-language", clientConfigHandlers.UpdateInterfaceLanguage)

		// Playlist privacies (public, unauthenticated)
		r.Route("/video-playlists", func(r chi.Router) {
			ph := social.NewPlaylistHandlers(deps.PlaylistService)
			r.Get("/privacies", ph.GetPrivacies)
		})

		// PeerTube-compatible handle-based account routes
		r.Route("/accounts", func(r chi.Router) {
			accountHandlers := account.NewAccountHandlers(deps.UserRepo, deps.VideoRepo, deps.ChannelService, deps.SubRepo)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/", accountHandlers.ListAccounts)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{name}", accountHandlers.GetAccount)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{name}/videos", accountHandlers.GetAccountVideos)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{name}/video-channels", accountHandlers.GetAccountVideoChannels)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{name}/ratings", accountHandlers.GetAccountRatings)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{name}/followers", accountHandlers.GetAccountFollowers)
		})

		if deps.PluginManager != nil && deps.PluginRepo != nil {
			ph := pluginhandlers.NewPluginHandler(deps.PluginRepo, deps.PluginManager, nil, false)
			pih := pluginhandlers.NewPluginInstallHandlers(deps.PluginManager)

			r.Route("/plugins", func(r chi.Router) {
				r.Use(middleware.Auth(cfg.JWTSecret))
				r.Use(middleware.RequireRole(string(domain.RoleAdmin), string(domain.RoleMod)))
				r.Get("/", ph.ListPlugins)
				r.Get("/available", pih.ListAvailablePlugins)
				r.Post("/install", ph.InstallPluginFromURL)
				r.Post("/update", ph.UpdatePluginFromURL)
				r.Post("/uninstall", ph.UninstallPluginCanonical)
				r.Get("/{npmName}/registered-settings", ph.GetRegisteredSettings)
				r.Get("/{npmName}/public-settings", ph.GetPublicSettings)
				r.Put("/{npmName}/settings", ph.UpdateCanonicalSettings)
				r.Get("/{npmName}", ph.GetPlugin)
			})
		}

		r.Route("/runners", func(r chi.Router) {
			if deps.RunnerRepo != nil {
				runnerHandlers := runnerhandlers.NewHandlers(deps.RunnerRepo, deps.EncodingRepo)
				adminRunners := r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole(string(domain.RoleAdmin), string(domain.RoleMod)))

				r.Post("/register", runnerHandlers.RegisterRunner)
				r.Post("/unregister", runnerHandlers.UnregisterRunner)

				adminRunners.Get("/", runnerHandlers.ListRunners)
				adminRunners.Get("/registration-tokens", runnerHandlers.ListRegistrationTokens)
				adminRunners.Post("/registration-tokens/generate", runnerHandlers.CreateRegistrationToken)
				adminRunners.Delete("/registration-tokens/{id}", runnerHandlers.DeleteRegistrationToken)
				adminRunners.Get("/jobs", runnerHandlers.ListJobs)
				adminRunners.Post("/jobs/{jobUUID}/cancel", runnerHandlers.CancelJob)
				adminRunners.Delete("/jobs/{jobUUID}", runnerHandlers.DeleteJob)
				adminRunners.Delete("/{runnerId}", runnerHandlers.DeleteRunner)

				r.Post("/jobs/request", runnerHandlers.RequestJob)
				r.Post("/jobs/{jobUUID}/accept", runnerHandlers.AcceptJob)
				r.Post("/jobs/{jobUUID}/abort", runnerHandlers.AbortJob)
				r.Post("/jobs/{jobUUID}/update", runnerHandlers.UpdateJob)
				r.Post("/jobs/{jobUUID}/error", runnerHandlers.ErrorJob)
				r.Post("/jobs/{jobUUID}/success", runnerHandlers.SuccessJob)
				r.Post("/jobs/{jobUUID}/files/videos/{videoId}/max-quality/audio", runnerHandlers.UploadJobFile)
				r.Post("/jobs/{jobUUID}/files/videos/{videoId}/max-quality", runnerHandlers.UploadJobFile)
				r.Post("/jobs/{jobUUID}/files/videos/{videoId}/thumbnails/max-quality", runnerHandlers.UploadJobFile)
				r.Post("/jobs/{jobUUID}/files/videos/{videoId}/previews/max-quality", runnerHandlers.UploadJobFile)
				r.Post("/jobs/{jobUUID}/files/videos/{videoId}/studio/task-files/{filename}", runnerHandlers.UploadJobFile)
			} else {
				runnersNotImplemented := compat.PeerTubeNotImplemented("PeerTube remote runners")
				adminRunners := r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole(string(domain.RoleAdmin), string(domain.RoleMod)))

				r.Post("/register", runnersNotImplemented)
				r.Post("/unregister", runnersNotImplemented)

				adminRunners.Get("/", runnersNotImplemented)
				adminRunners.Get("/registration-tokens", runnersNotImplemented)
				adminRunners.Post("/registration-tokens/generate", runnersNotImplemented)
				adminRunners.Delete("/registration-tokens/{id}", runnersNotImplemented)
				adminRunners.Get("/jobs", runnersNotImplemented)
				adminRunners.Post("/jobs/{jobUUID}/cancel", runnersNotImplemented)
				adminRunners.Delete("/jobs/{jobUUID}", runnersNotImplemented)
				adminRunners.Delete("/{runnerId}", runnersNotImplemented)

				r.Post("/jobs/request", runnersNotImplemented)
				r.Post("/jobs/{jobUUID}/accept", runnersNotImplemented)
				r.Post("/jobs/{jobUUID}/abort", runnersNotImplemented)
				r.Post("/jobs/{jobUUID}/update", runnersNotImplemented)
				r.Post("/jobs/{jobUUID}/error", runnersNotImplemented)
				r.Post("/jobs/{jobUUID}/success", runnersNotImplemented)
				r.Post("/jobs/{jobUUID}/files/videos/{videoId}/max-quality/audio", runnersNotImplemented)
				r.Post("/jobs/{jobUUID}/files/videos/{videoId}/max-quality", runnersNotImplemented)
				r.Post("/jobs/{jobUUID}/files/videos/{videoId}/thumbnails/max-quality", runnersNotImplemented)
				r.Post("/jobs/{jobUUID}/files/videos/{videoId}/previews/max-quality", runnersNotImplemented)
				r.Post("/jobs/{jobUUID}/files/videos/{videoId}/studio/task-files/{filename}", runnersNotImplemented)
			}
		})

		r.Route("/messages", func(r chi.Router) {
			r.Use(middleware.Auth(cfg.JWTSecret))
			r.Post("/", messaging.SendMessageHandler(deps.MessageService))
			r.Get("/", messaging.GetMessagesHandler(deps.MessageService))
			r.Put("/{messageId}/read", messaging.MarkMessageReadHandler(deps.MessageService))
			r.Delete("/{messageId}", messaging.DeleteMessageHandler(deps.MessageService))
		})

		r.Route("/conversations", func(r chi.Router) {
			r.Use(middleware.Auth(cfg.JWTSecret))
			r.Get("/", messaging.GetConversationsHandler(deps.MessageService))
			r.Get("/unread-count", messaging.GetUnreadCountHandler(deps.MessageService))
		})

		if deps.E2EEService != nil {
			e2eeHandler := messaging.NewSecureMessagesHandler(deps.E2EEService, govalidator.New())
			r.Route("/e2ee", func(r chi.Router) {
				r.Use(middleware.Auth(cfg.JWTSecret))
				r.Post("/keys", e2eeHandler.RegisterIdentityKey)
				r.Get("/keys/{userId}", e2eeHandler.GetPublicKeys)
				r.Get("/status", e2eeHandler.GetE2EEStatus)
				r.Post("/key-exchange", e2eeHandler.InitiateKeyExchange)
				r.Post("/key-exchange/accept", e2eeHandler.AcceptKeyExchange)
				r.Get("/key-exchange/pending", e2eeHandler.GetPendingKeyExchanges)
				r.Post("/messages", e2eeHandler.StoreEncryptedMessage)
				r.Get("/messages/{conversationId}", e2eeHandler.GetEncryptedMessages)
			})
		}

		r.Get("/trending", viewsHandler.GetTrendingVideos)

		r.Post("/views/fingerprint", viewsHandler.GenerateFingerprint)
		r.With(middleware.Auth(cfg.JWTSecret)).Get("/views/history", viewsHandler.GetViewHistory)
		r.With(middleware.Auth(cfg.JWTSecret)).Get("/users/me/history/videos", viewsHandler.GetViewHistory)
		r.With(middleware.Auth(cfg.JWTSecret)).Delete("/users/me/history/videos", viewsHandler.ClearWatchHistory)
		r.With(middleware.Auth(cfg.JWTSecret)).Delete("/users/me/history/videos/{videoId}", viewsHandler.RemoveVideoFromHistory)

		r.Route("/channels", func(r chi.Router) {
			channelHandlers := channel.NewChannelHandlers(deps.ChannelService, deps.SubRepo)

			r.Get("/", channelHandlers.ListChannels)
			r.Get("/{id}", channelHandlers.GetChannel)
			r.Get("/{id}/videos", channelHandlers.GetChannelVideos)
			r.Get("/{id}/subscribers", channelHandlers.GetChannelSubscribers)

			r.Group(func(r chi.Router) {
				r.Use(middleware.Auth(cfg.JWTSecret))
				r.Post("/", channelHandlers.CreateChannel)
				r.Put("/{id}", channelHandlers.UpdateChannel)
				r.Delete("/{id}", channelHandlers.DeleteChannel)
				r.Post("/{id}/subscribe", channelHandlers.SubscribeToChannel)
				r.Delete("/{id}/subscribe", channelHandlers.UnsubscribeFromChannel)

				if deps.ChannelRepo != nil {
					channelMediaHandlers := channel.NewChannelMediaHandlers(deps.ChannelRepo)
					r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/avatar", channelMediaHandlers.UploadAvatar)
					r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{id}/avatar", channelMediaHandlers.DeleteAvatar)
					r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/banner", channelMediaHandlers.UploadBanner)
					r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{id}/banner", channelMediaHandlers.DeleteBanner)
				}
			})
		})

		r.Route("/search", func(r chi.Router) {
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/videos", video.SearchVideosHandler(deps.VideoRepo))
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/video-channels", video.SearchChannelsHandler(deps.ChannelRepo))
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/video-playlists", video.SearchPlaylistsHandler(deps.PlaylistRepo))
		})

		if deps.LiveStreamRepo != nil && deps.StreamKeyRepo != nil && deps.ViewerSessionRepo != nil {
			registerLiveStreamAPIRoutes(r, deps, cfg)
		}

		if deps.ChatServer != nil && deps.ChatRepo != nil {
			log.Printf("Registering chat routes...")
			chatHandlers := messaging.NewChatHandlers(deps.ChatServer, deps.ChatRepo, deps.LiveStreamRepo, deps.UserRepo, deps.SubRepo)
			chatHandlers.RegisterRoutes(r, cfg.JWTSecret)
		}

		if deps.SocialService != nil {
			log.Printf("Registering social routes...")
			socialHandler := social.NewSocialHandler(deps.SocialService)
			socialHandler.RegisterRoutes(r, cfg.JWTSecret)
		}

		r.Route("/comments", func(r chi.Router) {
			commentHandlers := social.NewCommentHandlers(deps.CommentService)
			r.Get("/{commentId}", commentHandlers.GetComment)
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/{commentId}", commentHandlers.UpdateComment)
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{commentId}", commentHandlers.DeleteComment)
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{commentId}/flag", commentHandlers.FlagComment)
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{commentId}/flag", commentHandlers.UnflagComment)
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{commentId}/moderate", commentHandlers.ModerateComment)
		})

		r.Route("/notifications", func(r chi.Router) {
			r.Use(middleware.Auth(cfg.JWTSecret))
			notificationHandlers := messaging.NewNotificationHandlers(deps.NotificationService)
			r.Get("/", notificationHandlers.GetNotifications)
			r.Get("/unread-count", notificationHandlers.GetUnreadCount)
			r.Get("/stats", notificationHandlers.GetNotificationStats)
			r.Put("/{id}/read", notificationHandlers.MarkAsRead)
			r.Put("/read-all", notificationHandlers.MarkAllAsRead)
			r.Post("/read", notificationHandlers.MarkBatchAsRead)
			r.Delete("/{id}", notificationHandlers.DeleteNotification)
		})

		if cfg.EnableIOTA && deps.PaymentService != nil {
			log.Printf("Registering IOTA payment routes...")
			r.Route("/payments", func(r chi.Router) {
				r.Use(middleware.Auth(cfg.JWTSecret))
				paymentHandler := payments.NewPaymentHandler(deps.PaymentService)
				r.Post("/wallet", paymentHandler.CreateWallet)
				r.Get("/wallet", paymentHandler.GetWallet)
				r.Post("/intents", paymentHandler.CreatePaymentIntent)
				r.Get("/intents/{id}", paymentHandler.GetPaymentIntent)
				r.Get("/transactions", paymentHandler.GetTransactionHistory)
			})
		}

		if deps.IPFSStreamingService != nil {
			r.Route("/ipfs", func(r chi.Router) {
				ipfsMetricsHandlers := video.NewIPFSMetricsHandlers(deps.IPFSStreamingService)
				r.Get("/metrics", ipfsMetricsHandlers.GetMetrics)
				r.Get("/gateways", ipfsMetricsHandlers.GetGatewayHealth)
			})
		}

		r.Route("/federation", func(r chi.Router) {
			fedHandlers := federation.NewFederationHandlers(deps.FederationRepo)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/timeline", fedHandlers.GetTimeline)
		})

		if deps.ServerFollowingRepo != nil {
			sfHandlers := federation.NewServerFollowingHandlers(deps.ServerFollowingRepo)
			r.Get("/server/followers", sfHandlers.ListFollowers)
			r.Get("/server/following", sfHandlers.ListFollowing)
			r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole(string(domain.RoleAdmin))).Post("/server/following", sfHandlers.FollowInstance)
			r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole(string(domain.RoleAdmin))).Delete("/server/following/{host}", sfHandlers.UnfollowInstance)
			r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole(string(domain.RoleAdmin))).Post("/server/followers/{host}/accept", sfHandlers.AcceptFollower)
			r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole(string(domain.RoleAdmin))).Post("/server/followers/{host}/reject", sfHandlers.RejectFollower)
			r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole(string(domain.RoleAdmin))).Delete("/server/followers/{host}", sfHandlers.DeleteFollower)
		}
		r.Post("/server/contact", misc.ContactFormHandler())

		// Server debug endpoints (admin only)
		debugHandlers := admin.NewDebugHandlers()
		r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole(string(domain.RoleAdmin))).Get("/server/debug", debugHandlers.GetDebugInfo)
		r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole(string(domain.RoleAdmin))).Post("/server/debug/run-command", debugHandlers.RunCommand)

		// Server log endpoints
		if logRepo, ok := deps.LogRepo.(admin.LogRepository); ok {
			logHandlers := admin.NewLogHandlers(logRepo)
			r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole(string(domain.RoleAdmin))).Get("/server/logs", logHandlers.GetServerLogs)
			r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole(string(domain.RoleAdmin))).Get("/server/audit-logs", logHandlers.GetAuditLogs)
			r.Post("/server/logs/client", logHandlers.CreateClientLog)
		}

		r.Get("/oauth-clients/local", misc.GetOAuthLocalHandler("local", cfg.JWTSecret))

		r.Route("/playlists", func(r chi.Router) {
			playlistHandlers := social.NewPlaylistHandlers(deps.PlaylistService)

			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/", playlistHandlers.ListPlaylists)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}", playlistHandlers.GetPlaylist)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}/items", playlistHandlers.GetPlaylistItems)

			r.With(middleware.Auth(cfg.JWTSecret)).Post("/", playlistHandlers.CreatePlaylist)
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/{id}", playlistHandlers.UpdatePlaylist)
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{id}", playlistHandlers.DeletePlaylist)
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/items", playlistHandlers.AddVideoToPlaylist)
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{id}/items/{itemId}", playlistHandlers.RemoveVideoFromPlaylist)
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/{id}/items/{itemId}/reorder", playlistHandlers.ReorderPlaylistItem)
		})

		moderationHandlers := moderation.NewModerationHandlers(deps.ModerationRepo)
		instanceHandlers := admin.NewInstanceHandlers(deps.ModerationRepo, deps.UserRepo, deps.VideoRepo)

		registerModerationAPIRoutes(r, deps, cfg, moderationHandlers)
		registerAdminAPIRoutes(r, deps, cfg, authHandlers, moderationHandlers, instanceHandlers, viewsHandler)

		r.Route("/config", func(r chi.Router) {
			r.Get("/", instanceHandlers.GetPublicConfig)
			r.Get("/about", instanceHandlers.GetInstanceAboutPublic)
			configResetHandlers := admin.NewConfigResetHandlers(deps.ModerationRepo)
			r.With(middleware.Auth(cfg.JWTSecret)).With(middleware.RequireRole(string(domain.RoleAdmin))).Get("/custom", configResetHandlers.GetCustomConfig)
			r.With(middleware.Auth(cfg.JWTSecret)).With(middleware.RequireRole(string(domain.RoleAdmin))).Put("/custom", configResetHandlers.UpdateCustomConfig)
			r.With(middleware.Auth(cfg.JWTSecret)).With(middleware.RequireRole(string(domain.RoleAdmin))).Delete("/custom", configResetHandlers.DeleteCustomConfig)
			instanceMedia := admin.NewInstanceMediaHandlers(deps.ModerationRepo)
			r.With(middleware.Auth(cfg.JWTSecret)).With(middleware.RequireRole(string(domain.RoleAdmin))).Post("/instance-avatar/pick", instanceMedia.UploadInstanceAvatar)
			r.With(middleware.Auth(cfg.JWTSecret)).With(middleware.RequireRole(string(domain.RoleAdmin))).Delete("/instance-avatar/pick", instanceMedia.DeleteInstanceAvatar)
			r.With(middleware.Auth(cfg.JWTSecret)).With(middleware.RequireRole(string(domain.RoleAdmin))).Post("/instance-banner/pick", instanceMedia.UploadInstanceBanner)
			r.With(middleware.Auth(cfg.JWTSecret)).With(middleware.RequireRole(string(domain.RoleAdmin))).Delete("/instance-banner/pick", instanceMedia.DeleteInstanceBanner)
		})

		r.Route("/custom-pages", func(r chi.Router) {
			configHandlers := admin.NewConfigResetHandlers(deps.ModerationRepo)
			r.Get("/homepage/instance", configHandlers.GetCustomHomepage)
			r.With(middleware.Auth(cfg.JWTSecret)).With(middleware.RequireRole(string(domain.RoleAdmin))).Put("/homepage/instance", configHandlers.UpdateCustomHomepage)
		})

		r.Route("/instance", func(r chi.Router) {
			r.Get("/about", instanceHandlers.GetInstanceAbout)
			r.Get("/stats", instanceHandlers.GetPublicStats)
		})

		registerPeerTubeAliasRoutes(r, deps, cfg)
	})

	registerExternalFeatureRoutes(r, deps, cfg.JWTSecret)
	registerPeerTubeCompatRoutes(r, deps, cfg)

	r.Get("/oembed", admin.NewInstanceHandlers(deps.ModerationRepo, deps.UserRepo, deps.VideoRepo).OEmbed)

	r.Get("/.well-known/atproto-did", admin.NewInstanceHandlers(deps.ModerationRepo, deps.UserRepo, deps.VideoRepo).WellKnownAtprotoDID)

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("NOT_FOUND %s %s", r.Method, r.URL.Path)
		shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("NOT_FOUND", "The requested resource was not found"))
	})

	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		shared.WriteError(w, http.StatusMethodNotAllowed, domain.NewDomainError("METHOD_NOT_ALLOWED", "Method not allowed for this endpoint"))
	})

	if lvl := strings.ToLower(cfg.LogLevel); lvl == "debug" || lvl == "trace" {
		_ = chi.Walk(r, func(method string, route string, handler http.Handler, middlewares ...func(http.Handler) http.Handler) error {
			log.Printf("ROUTE %s %s", method, route)
			return nil
		})
	}
}

func registerExternalFeatureRoutes(r chi.Router, deps *shared.HandlerDependencies, jwtSecret string) {
	if deps.LiveStreamRepo != nil {
		log.Printf("Registering waiting room routes...")
		waitingRoomHandlers := livestream.NewWaitingRoomHandler(
			newWaitingRoomAdapter(deps.LiveStreamRepo, deps.ChannelRepo),
			deps.UserRepo,
		)
		waitingRoomHandlers.RegisterWaitingRoomRoutes(r, jwtSecret)
	}

	if deps.RedundancyService != nil {
		log.Printf("Registering redundancy admin routes...")
		redundancySvc, _ := deps.RedundancyService.(federation.RedundancyServiceInterface)
		discoverySvc, _ := deps.InstanceDiscovery.(federation.InstanceDiscoveryInterface)
		if redundancySvc != nil {
			rh := federation.NewRedundancyHandler(redundancySvc, discoverySvc)
			rh.RegisterRoutes(r, jwtSecret)
		}
	}

	if deps.VideoCategoryUseCase != nil {
		log.Printf("Registering video category routes...")
		categoryHandler := video.NewVideoCategoryHandler(deps.VideoCategoryUseCase)
		categoryHandler.RegisterRoutes(r, jwtSecret)
	}

	if deps.AnalyticsRepo != nil && deps.LiveStreamRepo != nil {
		log.Printf("Registering analytics routes...")
		analyticsCollector, _ := deps.AnalyticsCollector.(video.AnalyticsCollectorInterface)
		analyticsHandler := video.NewAnalyticsHandler(
			newWaitingRoomAdapter(deps.LiveStreamRepo, deps.ChannelRepo),
			deps.AnalyticsRepo,
			analyticsCollector,
		)
		analyticsHandler.RegisterRoutes(r, jwtSecret)
	}
}

type waitingRoomAdapter struct {
	ls repository.LiveStreamRepository
	ch *repository.ChannelRepository
}

func newWaitingRoomAdapter(ls repository.LiveStreamRepository, ch *repository.ChannelRepository) *waitingRoomAdapter {
	return &waitingRoomAdapter{ls: ls, ch: ch}
}

func (a *waitingRoomAdapter) GetByID(ctx context.Context, id string) (*domain.LiveStream, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return nil, domain.ErrBadRequest
	}
	return a.ls.GetByID(ctx, uid)
}

func (a *waitingRoomAdapter) GetChannelByID(ctx context.Context, id uuid.UUID) (*domain.Channel, error) {
	if a.ch == nil {
		return nil, domain.ErrNotFound
	}
	return a.ch.GetByID(ctx, id)
}

func (a *waitingRoomAdapter) UpdateWaitingRoom(ctx context.Context, streamID uuid.UUID, enabled bool, message string) error {
	return a.ls.UpdateWaitingRoom(ctx, streamID, enabled, message)
}

func (a *waitingRoomAdapter) ScheduleStream(ctx context.Context, streamID uuid.UUID, scheduledStart *time.Time, scheduledEnd *time.Time, waitingRoomEnabled bool, waitingRoomMessage string) error {
	return a.ls.ScheduleStream(ctx, streamID, scheduledStart, scheduledEnd, waitingRoomEnabled, waitingRoomMessage)
}

func (a *waitingRoomAdapter) CancelSchedule(ctx context.Context, streamID uuid.UUID) error {
	return a.ls.CancelSchedule(ctx, streamID)
}

func (a *waitingRoomAdapter) GetScheduledStreams(ctx context.Context, limit, offset int) ([]*domain.LiveStream, error) {
	return a.ls.GetScheduledStreams(ctx, limit, offset)
}

func (a *waitingRoomAdapter) GetUpcomingStreams(ctx context.Context, userID uuid.UUID, limit int) ([]*domain.LiveStream, error) {
	return a.ls.GetUpcomingStreams(ctx, userID, limit)
}

// registerLiveStreamAPIRoutes registers /streams routes. Extracted to keep
// RegisterRoutesWithDependencies within cyclomatic-complexity limits.
func registerLiveStreamAPIRoutes(r chi.Router, deps *shared.HandlerDependencies, cfg *config.Config) {
	log.Printf("Registering live stream routes...")
	r.Route("/streams", func(r chi.Router) {
		liveStreamHandlers := livestream.NewLiveStreamHandlers(
			deps.LiveStreamRepo,
			deps.StreamKeyRepo,
			deps.ViewerSessionRepo,
			deps.ChannelRepo,
			deps.StreamManager,
			cfg,
		)

		var hlsHandlers *video.HLSHandlers
		if deps.HLSTranscoder != nil {
			hlsHandlers = video.NewHLSHandlers(cfg, deps.LiveStreamRepo, deps.HLSTranscoder, deps.IPFSStreamingService)
		}

		r.Group(func(r chi.Router) {
			r.Use(middleware.Auth(cfg.JWTSecret))
			r.Post("/", liveStreamHandlers.CreateStream)
			r.Get("/active", liveStreamHandlers.GetActiveStreams)
		})

		r.Route("/channels/{channelId}", func(r chi.Router) {
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/", liveStreamHandlers.ListChannelStreams)
		})

		r.Route("/{id}", func(r chi.Router) {
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/", liveStreamHandlers.GetStream)
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/", liveStreamHandlers.UpdateStream)
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/end", liveStreamHandlers.EndStream)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/stats", liveStreamHandlers.GetStreamStats)
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/rotate-key", liveStreamHandlers.RotateStreamKey)

			if deps.LiveStreamSessionRepo != nil {
				sessionHandlers := livestream.NewSessionHistoryHandlers(deps.LiveStreamSessionRepo)
				r.With(middleware.Auth(cfg.JWTSecret)).Get("/sessions", sessionHandlers.GetSessionHistory)
			}

			if hlsHandlers != nil {
				r.Route("/hls", func(r chi.Router) {
					r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/master.m3u8", hlsHandlers.GetMasterPlaylist)
					r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{variant}/index.m3u8", hlsHandlers.GetVariantPlaylist)
					r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{variant}/{segment}", hlsHandlers.GetSegment)
				})
				r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/hls-info", hlsHandlers.GetStreamHLSInfo)
			}
		})
	})
}

// registerModerationAPIRoutes registers abuse reports, blocklist and video
// moderation routes. Extracted to keep RegisterRoutesWithDependencies within
// cyclomatic-complexity limits.
func registerModerationAPIRoutes(r chi.Router, deps *shared.HandlerDependencies, cfg *config.Config, moderationHandlers *moderation.ModerationHandlers) {
	r.Route("/abuse-reports", func(r chi.Router) {
		r.With(middleware.Auth(cfg.JWTSecret)).Post("/", moderationHandlers.CreateAbuseReport)
	})

	if deps.AbuseMessageRepo != nil {
		abuseMessageHandlers := moderation.NewAbuseMessageHandlers(deps.AbuseMessageRepo)
		r.Route("/admin/abuse-reports/{id}/messages", func(r chi.Router) {
			r.Use(middleware.Auth(cfg.JWTSecret))
			r.Get("/", abuseMessageHandlers.ListMessages)
			r.Post("/", abuseMessageHandlers.CreateMessage)
			r.Delete("/{messageId}", abuseMessageHandlers.DeleteMessage)
		})
	}

	if deps.ChapterRepo != nil {
		chapterHandlers := video.NewChapterHandlers(deps.ChapterRepo, deps.VideoRepo)
		r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/videos/{id}/chapters", chapterHandlers.GetChapters)
		r.With(middleware.Auth(cfg.JWTSecret)).Put("/videos/{id}/chapters", chapterHandlers.PutChapters)
	}

	r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/videos/{id}/description", video.GetVideoDescriptionHandler(deps.VideoRepo))

	// Video passwords
	if deps.VideoPasswordRepo != nil {
		passwordHandlers := video.NewPasswordHandlers(deps.VideoPasswordRepo, deps.VideoRepo)
		r.Route("/videos/{id}/passwords", func(r chi.Router) {
			r.Use(middleware.Auth(cfg.JWTSecret))
			r.Get("/", passwordHandlers.ListPasswords)
			r.Put("/", passwordHandlers.ReplacePasswords)
			r.Post("/", passwordHandlers.AddPassword)
			r.Delete("/{passwordId}", passwordHandlers.DeletePassword)
		})
	}

	// Video storyboards
	if deps.VideoStoryboardRepo != nil {
		storyboardHandlers := video.NewStoryboardHandlers(deps.VideoStoryboardRepo)
		r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/videos/{id}/storyboards", storyboardHandlers.ListStoryboards)
	}

	// Video embed privacy
	if deps.VideoEmbedRepo != nil {
		embedHandlers := video.NewEmbedPrivacyHandlers(deps.VideoEmbedRepo, deps.VideoRepo)
		r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/videos/{id}/embed-privacy", embedHandlers.GetEmbedPrivacy)
		r.With(middleware.Auth(cfg.JWTSecret)).Put("/videos/{id}/embed-privacy", embedHandlers.UpdateEmbedPrivacy)
		r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/videos/{id}/embed-privacy/allowed", embedHandlers.CheckDomainAllowed)
	}

	// Video source replacement
	{
		sourceReplaceHandlers := video.NewSourceReplaceHandlers(deps.VideoRepo)
		r.Route("/videos/{id}/source/replace-resumable", func(r chi.Router) {
			r.Use(middleware.Auth(cfg.JWTSecret))
			r.Post("/", sourceReplaceHandlers.InitiateReplace)
			r.Put("/", sourceReplaceHandlers.UploadReplaceChunk)
			r.Delete("/", sourceReplaceHandlers.CancelReplace)
		})
	}

	// Video studio editing
	if deps.StudioService != nil {
		if studioSvc, ok := deps.StudioService.(video.StudioService); ok {
			studioHandlers := video.NewStudioHandlers(studioSvc, deps.VideoRepo)
			r.Route("/videos/{id}/studio", func(r chi.Router) {
				r.Use(middleware.Auth(cfg.JWTSecret))
				r.Post("/edit", studioHandlers.CreateEditJob)
				r.Get("/jobs", studioHandlers.ListJobs)
				r.Get("/jobs/{jobId}", studioHandlers.GetJob)
			})
		}
	}

	// Video file management
	{
		fileMgmtHandlers := video.NewFileManagementHandlers(deps.VideoRepo)
		r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/videos/{id}/metadata/{videoFileId}", fileMgmtHandlers.GetFileMetadata)
		r.With(middleware.Auth(cfg.JWTSecret)).Delete("/videos/{id}/hls", fileMgmtHandlers.DeleteAllHLS)
		r.With(middleware.Auth(cfg.JWTSecret)).Delete("/videos/{id}/hls/{videoFileId}", fileMgmtHandlers.DeleteHLSFile)
		r.With(middleware.Auth(cfg.JWTSecret)).Delete("/videos/{id}/web-videos", fileMgmtHandlers.DeleteAllWebVideos)
		r.With(middleware.Auth(cfg.JWTSecret)).Delete("/videos/{id}/web-videos/{videoFileId}", fileMgmtHandlers.DeleteWebVideoFile)
	}

	// Video overviews
	{
		overviewHandlers := video.NewOverviewHandlers(deps.VideoRepo)
		r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/overviews/videos", overviewHandlers.GetOverview)
	}

	if deps.BlacklistRepo != nil {
		blacklistHandlers := moderation.NewBlacklistHandlers(deps.BlacklistRepo)
		r.Route("/videos/{id}/blacklist", func(r chi.Router) {
			r.Use(middleware.Auth(cfg.JWTSecret))
			r.Use(middleware.RequireRole(string(domain.RoleAdmin), string(domain.RoleMod)))
			r.Post("/", blacklistHandlers.AddToBlacklist)
			r.Delete("/", blacklistHandlers.RemoveFromBlacklist)
		})
		r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole(string(domain.RoleAdmin), string(domain.RoleMod))).
			Get("/videos/blacklist", blacklistHandlers.ListBlacklist)
	}

	if deps.ModerationRepo != nil {
		r.With(middleware.Auth(cfg.JWTSecret)).Get("/blocklist/status", moderation.BlocklistStatusHandler(deps.ModerationRepo))
	}

	if deps.UserBlockRepo != nil {
		userBlockHandlers := moderation.NewUserBlocklistHandlers(deps.UserBlockRepo)
		r.Route("/blocklist/accounts", func(r chi.Router) {
			r.Use(middleware.Auth(cfg.JWTSecret))
			r.Get("/", userBlockHandlers.ListAccountBlocks)
			r.Post("/", userBlockHandlers.BlockAccount)
			r.Delete("/{accountName}", userBlockHandlers.UnblockAccount)
		})
		r.Route("/blocklist/servers", func(r chi.Router) {
			r.Use(middleware.Auth(cfg.JWTSecret))
			r.Get("/", userBlockHandlers.ListServerBlocks)
			r.Post("/", userBlockHandlers.BlockServer)
			r.Delete("/{host}", userBlockHandlers.UnblockServer)
		})
	}

	// Watched words routes
	if deps.WatchedWordsService != nil {
		wwHandlers := watchedwords.NewHandlers(deps.WatchedWordsService)
		r.Route("/watched-words", func(r chi.Router) {
			r.Route("/accounts/{accountName}/lists", func(r chi.Router) {
				r.Use(middleware.Auth(cfg.JWTSecret))
				r.Get("/", wwHandlers.ListAccountWatchedWords)
				r.Post("/", wwHandlers.CreateAccountWatchedWordList)
				r.Put("/{listId}", wwHandlers.UpdateAccountWatchedWordList)
				r.Delete("/{listId}", wwHandlers.DeleteAccountWatchedWordList)
			})
			r.Route("/server/lists", func(r chi.Router) {
				r.Use(middleware.Auth(cfg.JWTSecret))
				r.Use(middleware.RequireRole(string(domain.RoleAdmin), string(domain.RoleMod)))
				r.Get("/", wwHandlers.ListServerWatchedWords)
				r.Post("/", wwHandlers.CreateServerWatchedWordList)
				r.Put("/{listId}", wwHandlers.UpdateServerWatchedWordList)
				r.Delete("/{listId}", wwHandlers.DeleteServerWatchedWordList)
			})
		})
	}

	// Auto-tag routes
	if deps.AutoTagsService != nil {
		atHandlers := autotags.NewHandlers(deps.AutoTagsService)
		r.Route("/auto-tags", func(r chi.Router) {
			r.Route("/accounts/{accountName}", func(r chi.Router) {
				r.Use(middleware.Auth(cfg.JWTSecret))
				r.Get("/policies", atHandlers.GetAccountAutoTagPolicies)
				r.Put("/policies", atHandlers.UpdateAccountAutoTagPolicies)
				r.Get("/available", atHandlers.GetAccountAvailableTags)
			})
			r.Route("/server", func(r chi.Router) {
				r.Use(middleware.Auth(cfg.JWTSecret))
				r.Use(middleware.RequireRole(string(domain.RoleAdmin), string(domain.RoleMod)))
				r.Get("/policies", atHandlers.GetServerAutoTagPolicies)
				r.Put("/policies", atHandlers.UpdateServerAutoTagPolicies)
				r.Get("/available", atHandlers.GetServerAvailableTags)
			})
		})
	}

	// Comment approval and bulk operations
	commentModHandlers := moderation.NewCommentModerationHandlers(deps.CommentRepo)
	r.With(middleware.Auth(cfg.JWTSecret)).Post("/comments/{commentId}/approve", commentModHandlers.ApproveComment)
	r.Route("/bulk", func(r chi.Router) {
		r.Use(middleware.Auth(cfg.JWTSecret))
		r.Use(middleware.RequireRole(string(domain.RoleAdmin), string(domain.RoleMod)))
		r.Post("/comments/remove", commentModHandlers.BulkRemoveComments)
	})

	// User-facing abuse reports
	if deps.ModerationRepo != nil {
		r.With(middleware.Auth(cfg.JWTSecret)).Get("/users/me/abuses", moderationHandlers.ListMyAbuses)
	}
}

// registerAdminAPIRoutes registers /admin/* routes. Extracted to keep
// RegisterRoutesWithDependencies within cyclomatic-complexity limits.
func registerAdminAPIRoutes(
	r chi.Router,
	deps *shared.HandlerDependencies,
	cfg *config.Config,
	authHandlers *auth.AuthHandlers,
	moderationHandlers *moderation.ModerationHandlers,
	instanceHandlers *admin.InstanceHandlers,
	viewsHandler *video.ViewsHandler,
) {
	r.Route("/admin", func(r chi.Router) {
		r.Use(middleware.Auth(cfg.JWTSecret))
		r.Use(middleware.RequireRole(string(domain.RoleAdmin), string(domain.RoleMod)))

		adminUserHandlers := admin.NewAdminUserHandlers(deps.UserRepo)
		r.Get("/users", adminUserHandlers.ListUsers)
		r.Put("/users/{id}", adminUserHandlers.UpdateUser)
		r.Delete("/users/{id}", adminUserHandlers.DeleteUser)
		r.Post("/users/{id}/block", adminUserHandlers.BlockUser)
		r.Post("/users/{id}/unblock", adminUserHandlers.UnblockUser)

		if deps.RegistrationRepo != nil {
			regHandlers := admin.NewRegistrationHandlers(deps.RegistrationRepo, deps.UserRepo)
			r.Get("/registrations", regHandlers.ListRegistrations)
			r.Post("/registrations/{id}/accept", regHandlers.AcceptRegistration)
			r.Post("/registrations/{id}/reject", regHandlers.RejectRegistration)
			r.Delete("/registrations/{id}", regHandlers.DeleteRegistration)
		}

		adminVideoHandlers := admin.NewAdminVideoHandlers(deps.VideoRepo)
		r.Get("/videos", adminVideoHandlers.ListVideos)

		// Admin comment listing
		adminCommentHandlers := moderation.NewCommentModerationHandlers(deps.CommentRepo)
		r.Get("/comments", adminCommentHandlers.ListAllComments)

		jobHandlers := admin.NewJobHandlers(deps.EncodingRepo, deps.EncodingScheduler)
		r.Get("/jobs/{state}", jobHandlers.ListJobs)
		r.Post("/jobs/pause", jobHandlers.PauseJobs)
		r.Post("/jobs/resume", jobHandlers.ResumeJobs)

		r.Route("/abuse-reports", func(r chi.Router) {
			r.Get("/", moderationHandlers.ListAbuseReports)
			r.Get("/{id}", moderationHandlers.GetAbuseReport)
			r.Put("/{id}", moderationHandlers.UpdateAbuseReport)
			r.Delete("/{id}", moderationHandlers.DeleteAbuseReport)
		})

		r.Route("/blocklist", func(r chi.Router) {
			r.Post("/", moderationHandlers.CreateBlocklistEntry)
			r.Get("/", moderationHandlers.ListBlocklistEntries)
			r.Put("/{id}", moderationHandlers.UpdateBlocklistEntry)
			r.Delete("/{id}", moderationHandlers.DeleteBlocklistEntry)
		})

		r.Route("/views", func(r chi.Router) {
			r.Use(middleware.RequireRole("admin"))
			r.Post("/aggregate", viewsHandler.AdminAggregateStats)
			r.Post("/cleanup", viewsHandler.AdminCleanupOldData)
		})

		r.Route("/instance/config", func(r chi.Router) {
			r.Use(middleware.RequireRole("admin"))
			r.Get("/", instanceHandlers.ListInstanceConfigs)
			r.Get("/{key}", instanceHandlers.GetInstanceConfig)
			r.Put("/{key}", instanceHandlers.UpdateInstanceConfig)
		})

		r.Route("/oauth/clients", func(r chi.Router) {
			r.Use(middleware.RequireRole("admin"))
			r.Get("/", authHandlers.AdminListOAuthClients)
			r.Post("/", authHandlers.AdminCreateOAuthClient)
			r.Put("/{clientId}/secret", authHandlers.AdminRotateOAuthClientSecret)
			r.Delete("/{clientId}", authHandlers.AdminDeleteOAuthClient)
		})

		fedAdminHandlers := federation.NewAdminFederationHandlers(deps.FederationRepo)
		r.Route("/federation/jobs", func(r chi.Router) {
			r.Get("/", fedAdminHandlers.ListJobs)
			r.Get("/{id}", fedAdminHandlers.GetJob)
			r.Post("/{id}/retry", fedAdminHandlers.RetryJob)
			r.Delete("/{id}", fedAdminHandlers.DeleteJob)
		})

		fedActorsHandlers := federation.NewAdminFederationActorsHandlers(deps.FederationRepo)
		r.Route("/federation/actors", func(r chi.Router) {
			r.Get("/", fedActorsHandlers.ListActors)
			r.Post("/", fedActorsHandlers.UpsertActor)
			r.Put("/{actor}", fedActorsHandlers.UpdateActor)
			r.Delete("/{actor}", fedActorsHandlers.DeleteActor)
		})

		fh := federation.NewFederationHardeningHandler(deps.HardeningService)
		r.Route("/federation/hardening", func(r chi.Router) {
			r.Get("/dashboard", fh.GetDashboard)
			r.Get("/health", fh.GetHealthMetrics)
			r.Get("/dlq", fh.GetDLQJobs)
			r.Post("/dlq/{id}/retry", fh.RetryDLQJob)
			r.Route("/blocklist", func(r chi.Router) {
				r.Get("/instances", fh.GetInstanceBlocks)
				r.Post("/instances", fh.BlockInstance)
				r.Delete("/instances/{domain}", fh.UnblockInstance)
				r.Post("/actors", fh.BlockActor)
				r.Get("/check", fh.CheckBlocked)
			})
			r.Route("/abuse", func(r chi.Router) {
				r.Get("/reports", fh.GetAbuseReports)
				r.Post("/reports/{id}/resolve", fh.ResolveAbuseReport)
			})
			r.Post("/cleanup", fh.RunCleanup)
		})

		if deps.BackupService != nil {
			backupHandler := backuphandlers.NewHandler(deps.BackupService)
			r.Route("/backups", func(r chi.Router) {
				r.Get("/", backupHandler.ListBackups)
				r.Post("/", backupHandler.TriggerBackup)
				r.Delete("/{id}", backupHandler.DeleteBackup)
				r.Post("/{id}/restore", backupHandler.RestoreBackup)
				r.Get("/restore/status", backupHandler.GetRestoreStatus)
			})
		}

		if deps.PluginManager != nil && deps.PluginRepo != nil {
			log.Printf("Registering plugin admin routes...")
			ph := pluginhandlers.NewPluginHandler(deps.PluginRepo, deps.PluginManager, nil, false)
			pih := pluginhandlers.NewPluginInstallHandlers(deps.PluginManager)
			r.Route("/plugins", func(r chi.Router) {
				r.Get("/", ph.ListPlugins)
				r.Get("/{name}", ph.GetPlugin)
				r.Post("/{name}/enable", ph.EnablePlugin)
				r.Post("/{name}/disable", ph.DisablePlugin)
				r.Put("/{name}/config", ph.UpdatePluginConfig)
				r.Delete("/{name}", ph.UninstallPlugin)
				r.Get("/statistics", ph.GetAllStatistics)
				r.Post("/upload", ph.UploadPlugin)
				r.Post("/install", ph.InstallPluginFromURL)
				r.Get("/available", pih.ListAvailablePlugins)
			})
		}

		if deps.MigrationService != nil {
			log.Printf("Registering migration routes...")
			migHandlers := migrationhandlers.NewMigrationHandlers(deps.MigrationService)
			r.Route("/migrations", func(r chi.Router) {
				r.Post("/peertube", migHandlers.StartMigration)
				r.Get("/", migHandlers.ListMigrations)
				r.Get("/{id}", migHandlers.GetMigration)
				r.Delete("/{id}", migHandlers.CancelMigration)
				r.Post("/{id}/dry-run", migHandlers.DryRun)
			})
		}
	})
}

// registerPeerTubeCompatRoutes registers static file serving and download routes
// outside /api/v1. These use PeerTube-compatible URL paths.
func registerPeerTubeCompatRoutes(r chi.Router, deps *shared.HandlerDependencies, cfg *config.Config) {
	staticH := statichandlers.NewHandlers(cfg, deps.VideoRepo)

	// Static web-video files
	r.Route("/static/web-videos", func(r chi.Router) {
		r.Get("/{filename}", staticH.ServeWebVideo)
		r.With(middleware.Auth(cfg.JWTSecret)).Get("/private/{filename}", staticH.ServePrivateWebVideo)
	})

	// Static HLS streaming playlists
	r.Route("/static/streaming-playlists/hls", func(r chi.Router) {
		r.Get("/{filename}", staticH.ServeHLSFile)
		r.With(middleware.Auth(cfg.JWTSecret)).Get("/private/{filename}", staticH.ServePrivateHLSFile)
	})

	// Video download endpoint
	r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/download/videos/generate/{videoId}", staticH.DownloadVideo)
}

// registerPeerTubeAliasRoutes registers PeerTube-compatible URL aliases inside the
// /api/v1 router. These are thin proxies that forward to existing handlers.
func registerPeerTubeAliasRoutes(r chi.Router, deps *shared.HandlerDependencies, cfg *config.Config) {
	// Note: Resumable upload aliases (POST/PUT/DELETE /videos/upload-resumable) and
	// category alias (GET /videos/categories) are registered inline in the
	// /videos Route block above to avoid wildcard conflicts with /{id}.

	// --- Notification aliases ---
	// PeerTube: /api/v1/users/me/notifications/* → our /api/v1/notifications/*
	if deps.NotificationService != nil {
		notificationHandlers := messaging.NewNotificationHandlers(deps.NotificationService)
		r.Route("/users/me/notifications", func(r chi.Router) {
			r.Use(middleware.Auth(cfg.JWTSecret))
			r.Get("/", notificationHandlers.GetNotifications)
			r.Get("/unread-count", notificationHandlers.GetUnreadCount)
			r.Get("/stats", notificationHandlers.GetNotificationStats)
			r.Put("/{id}/read", notificationHandlers.MarkAsRead)
			r.Put("/read-all", notificationHandlers.MarkAllAsRead)
			r.Post("/read-all", notificationHandlers.MarkAllAsRead) // PeerTube uses POST
			r.Post("/read", notificationHandlers.MarkBatchAsRead)
			r.Delete("/{id}", notificationHandlers.DeleteNotification)
		})
	}

	// --- Blocklist aliases ---
	// PeerTube: /api/v1/users/me/blocklist/* → our /api/v1/blocklist/*
	if deps.UserBlockRepo != nil {
		userBlockHandlers := moderation.NewUserBlocklistHandlers(deps.UserBlockRepo)
		r.Route("/users/me/blocklist", func(r chi.Router) {
			r.Use(middleware.Auth(cfg.JWTSecret))
			r.Route("/accounts", func(r chi.Router) {
				r.Get("/", userBlockHandlers.ListAccountBlocks)
				r.Post("/", userBlockHandlers.BlockAccount)
				r.Delete("/{accountName}", userBlockHandlers.UnblockAccount)
			})
			r.Route("/servers", func(r chi.Router) {
				r.Get("/", userBlockHandlers.ListServerBlocks)
				r.Post("/", userBlockHandlers.BlockServer)
				r.Delete("/{host}", userBlockHandlers.UnblockServer)
			})
		})
	}

	// --- Playlist alias ---
	// PeerTube: /api/v1/video-playlists/* → our /api/v1/playlists/*
	// Note: /video-playlists/privacies is already registered in a separate Route block.
	// Use individual route registrations to avoid shadowing that subrouter.
	if deps.PlaylistService != nil {
		playlistHandlers := social.NewPlaylistHandlers(deps.PlaylistService)
		r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/video-playlists", playlistHandlers.ListPlaylists)
		r.With(middleware.Auth(cfg.JWTSecret)).Post("/video-playlists", playlistHandlers.CreatePlaylist)
		r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/video-playlists/{id}", playlistHandlers.GetPlaylist)
		r.With(middleware.Auth(cfg.JWTSecret)).Put("/video-playlists/{id}", playlistHandlers.UpdatePlaylist)
		r.With(middleware.Auth(cfg.JWTSecret)).Delete("/video-playlists/{id}", playlistHandlers.DeletePlaylist)
		r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/video-playlists/{id}/items", playlistHandlers.GetPlaylistItems)
		r.With(middleware.Auth(cfg.JWTSecret)).Post("/video-playlists/{id}/items", playlistHandlers.AddVideoToPlaylist)
		r.With(middleware.Auth(cfg.JWTSecret)).Delete("/video-playlists/{id}/items/{itemId}", playlistHandlers.RemoveVideoFromPlaylist)
		r.With(middleware.Auth(cfg.JWTSecret)).Put("/video-playlists/{id}/items/{itemId}/reorder", playlistHandlers.ReorderPlaylistItem)
	}

	// --- Channel handle aliases ---
	// PeerTube: PUT/DELETE /api/v1/video-channels/{handle}
	if deps.ChannelService != nil {
		aliasHandlers := compat.NewAliasHandlers(deps.CaptionRepo, deps.ChannelService, deps.VideoRepo)
		channelHandlers := channel.NewChannelHandlers(deps.ChannelService, deps.SubRepo)

		r.With(middleware.Auth(cfg.JWTSecret)).Put("/video-channels/{handle}", aliasHandlers.ResolveChannelHandle(channelHandlers.UpdateChannel))
		r.With(middleware.Auth(cfg.JWTSecret)).Delete("/video-channels/{handle}", aliasHandlers.ResolveChannelHandle(channelHandlers.DeleteChannel))
	}

	// Note: Caption language aliases (PUT/DELETE /videos/{id}/captions/{captionLanguage})
	// are handled by the existing {captionId} handlers which accept both UUIDs and
	// language codes. See the caption handler's UpdateCaption/DeleteCaption methods.

	// --- Playback metrics ---
	playbackHandler := metricshandlers.NewPlaybackHandler()
	r.Post("/metrics/playback", playbackHandler.ReportPlaybackMetrics)
}
