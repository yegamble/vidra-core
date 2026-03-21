package httpapi

import (
	"athena/internal/httpapi/handlers/account"
	"athena/internal/httpapi/handlers/admin"
	"athena/internal/httpapi/handlers/auth"
	backuphandlers "athena/internal/httpapi/handlers/backup"
	"athena/internal/httpapi/handlers/channel"
	"athena/internal/httpapi/handlers/federation"
	"athena/internal/httpapi/handlers/livestream"
	"athena/internal/httpapi/handlers/messaging"
	"athena/internal/httpapi/handlers/moderation"
	"athena/internal/httpapi/handlers/payments"
	pluginhandlers "athena/internal/httpapi/handlers/plugin"
	"athena/internal/httpapi/handlers/social"
	"athena/internal/httpapi/handlers/video"
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

func RegisterRoutesWithDependencies(r chi.Router, cfg *config.Config, rlManager *middleware.RateLimiterManager, deps *shared.HandlerDependencies) {
	generalLimiter := rlManager.CreateRateLimiter(cfg.RateLimitDuration, cfg.RateLimitRequests)
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

	strictAuthLimiter := rlManager.CreateRateLimiter(60*time.Second, 5)
	strictLoginLimiter := rlManager.CreateRateLimiter(60*time.Second, 10)
	strictImportLimiter := rlManager.CreateRateLimiter(60*time.Second, 10)

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
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}", video.GetVideoHandler(deps.VideoRepo, deps.CaptionService))
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}/stream", video.StreamVideoHandler(deps.VideoRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/{id}/source", video.GetVideoSourceHandler(deps.VideoRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/subscriptions", channel.ListSubscriptionVideosHandler(deps.SubRepo))

			r.With(middleware.Auth(cfg.JWTSecret)).Post("/", video.CreateVideoHandler(deps.VideoRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/{id}", video.UpdateVideoHandler(deps.VideoRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{id}", video.DeleteVideoHandler(deps.VideoRepo))

			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/upload", video.VideoUploadChunkHandler(deps.UploadService, cfg))
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/complete", video.VideoCompleteUploadHandler(deps.UploadService))

			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Post("/{id}/views", viewsHandler.TrackView)
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/{id}/analytics", viewsHandler.GetVideoAnalytics)
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/{id}/stats/daily", viewsHandler.GetDailyStats)

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

		r.Route("/users", func(r chi.Router) {
			r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole("admin")).Post("/", auth.CreateUserHandler(deps.UserRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me", auth.GetCurrentUserHandler(deps.UserRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/me", auth.UpdateCurrentUserHandler(deps.UserRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/me", auth.DeleteAccountHandler(deps.UserRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/me/avatar", authHandlers.UploadAvatar)
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/me/avatar", authHandlers.DeleteAvatar)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}", auth.GetPublicUserHandler(deps.UserRepo))
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}/videos", video.GetUserVideosHandler(deps.VideoRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/subscribe", channel.SubscribeToUserHandler(deps.SubRepo, deps.UserRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{id}/subscribe", channel.UnsubscribeFromUserHandler(deps.SubRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me/subscriptions", channel.ListMySubscriptionsHandler(deps.SubRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me/subscriptions/exist", channel.CheckSubscriptionsExistHandler(deps.SubRepo, deps.ChannelService))

			channelHandlers := channel.NewChannelHandlers(deps.ChannelService, deps.SubRepo)
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me/channels", channelHandlers.GetMyChannels)

			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me/videos", video.GetMyVideosHandler(deps.VideoRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me/comments", video.GetMyCommentsHandler())

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
		})

		// PeerTube-compatible /video-channels/{channelHandle} routes
		r.Route("/video-channels", func(r chi.Router) {
			channelHandlers := channel.NewChannelHandlers(deps.ChannelService, deps.SubRepo)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{channelHandle}", channelHandlers.GetChannelByHandleParam)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{channelHandle}/videos", channelHandlers.GetChannelVideosByHandleParam)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{channelHandle}/video-playlists", social.GetChannelPlaylistsHandler(deps.ChannelService, deps.PlaylistService))
		})

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
			r.With(middleware.Auth(cfg.JWTSecret)).With(middleware.RequireRole(string(domain.RoleAdmin))).Delete("/custom", admin.NewConfigResetHandlers(deps.ModerationRepo).DeleteCustomConfig)
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
	})

	registerExternalFeatureRoutes(r, deps, cfg.JWTSecret)

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
		}

		adminVideoHandlers := admin.NewAdminVideoHandlers(deps.VideoRepo)
		r.Get("/videos", adminVideoHandlers.ListVideos)

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
				r.Post("/install", pih.InstallPlugin)
				r.Get("/available", pih.ListAvailablePlugins)
			})
		}
	})
}
