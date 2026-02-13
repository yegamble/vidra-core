package httpapi

import (
	"athena/internal/httpapi/handlers/admin"
	"athena/internal/httpapi/handlers/auth"
	"athena/internal/httpapi/handlers/channel"
	"athena/internal/httpapi/handlers/federation"
	"athena/internal/httpapi/handlers/livestream"
	"athena/internal/httpapi/handlers/messaging"
	"athena/internal/httpapi/handlers/moderation"
	"athena/internal/httpapi/handlers/payments"
	"athena/internal/httpapi/handlers/social"
	"athena/internal/httpapi/handlers/video"
	"athena/internal/httpapi/shared"
	"log"
	"net/http"
	"strings"
	"time"

	chi "github.com/go-chi/chi/v5"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/middleware"
	importuc "athena/internal/usecase/import"
)

// RegisterRoutesWithDependencies sets up all HTTP routes using pre-initialized dependencies.
// This function only handles route registration - all resources are already wired.
func RegisterRoutesWithDependencies(r chi.Router, cfg *config.Config, rlManager *middleware.RateLimiterManager, deps *shared.HandlerDependencies) {
	// Create rate limiters using the manager so they can be properly shut down
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

	// Enforce strict default request-body limits while allowing larger upload endpoints.
	r.Use(middleware.SizeLimiterWithOverrides(defaultMaxRequestBytes, []middleware.RequestSizeOverride{
		{PathPrefix: "/api/v1/uploads", MaxBytes: uploadMaxRequestBytes},
		{PathPrefix: "/api/v1/videos/", PathSuffix: "/upload", MaxBytes: uploadMaxRequestBytes},
		{PathPrefix: "/api/v1/users/me/avatar", MaxBytes: uploadMaxRequestBytes},
	}))

	// SECURITY: Create stricter rate limiters for critical endpoints
	// These prevent abuse of authentication and resource-intensive operations
	strictAuthLimiter := rlManager.CreateRateLimiter(60*time.Second, 5)    // 5 per minute for registration
	strictLoginLimiter := rlManager.CreateRateLimiter(60*time.Second, 10)  // 10 per minute for login
	strictImportLimiter := rlManager.CreateRateLimiter(60*time.Second, 10) // 10 per minute for imports

	// Create server instance with dependencies
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

	// Set TwoFAService if available
	if deps.TwoFAService != nil {
		server.SetTwoFAService(deps.TwoFAService)
	}

	// Create auth handlers instance for avatar and other auth-related routes
	authHandlers := auth.NewAuthHandlers(
		deps.UserRepo,
		deps.SessionRepo,
		deps.OAuthRepo,
		nil, // verificationService (can be set later if needed)
		deps.JWTSecret,
		deps.Redis,
		deps.RedisPingTimeout,
		deps.IPFSApi,
		deps.IPFSCluster,
		cfg,
	)

	// Register auth routes with appropriate middleware
	// SECURITY FIX: Apply stricter rate limiting to prevent account spam and brute force attacks
	r.With(strictAuthLimiter.Limit).Post("/auth/register", server.Register)
	r.With(strictLoginLimiter.Limit).Post("/auth/login", server.Login)
	r.Post("/auth/refresh", server.RefreshToken)
	r.With(middleware.Auth(cfg.JWTSecret)).Post("/auth/logout", server.Logout)

	// Two-Factor Authentication routes (require authentication)
	r.Route("/auth/2fa", func(r chi.Router) {
		r.Use(middleware.Auth(cfg.JWTSecret))
		twoFAHandlers := auth.NewTwoFAHandlers(deps.TwoFAService)
		r.Post("/setup", twoFAHandlers.SetupTwoFA)
		r.Post("/verify-setup", twoFAHandlers.VerifyTwoFASetup)
		r.Post("/disable", twoFAHandlers.DisableTwoFA)
		r.Post("/regenerate-backup-codes", twoFAHandlers.RegenerateBackupCodes)
		r.Get("/status", twoFAHandlers.GetTwoFAStatus)
	})

	// OAuth2 endpoints
	r.Post("/oauth/token", authHandlers.OAuthToken)
	r.HandleFunc("/oauth/authorize", authHandlers.OAuthAuthorize)
	r.Post("/oauth/revoke", authHandlers.OAuthRevoke)
	r.Post("/oauth/introspect", authHandlers.OAuthIntrospect)

	// Register health routes
	r.Get("/health", server.HealthCheck)
	r.Get("/ready", server.ReadinessCheck)

	// ActivityPub well-known endpoints (must be at root level, not under /api/v1)
	if cfg.EnableActivityPub && deps.ActivityPubService != nil {
		apHandlers := federation.NewActivityPubHandlers(deps.ActivityPubService, cfg, deps.UserRepo, deps.VideoRepo)

		// WebFinger and NodeInfo discovery
		r.Get("/.well-known/webfinger", apHandlers.WebFinger)
		r.Get("/.well-known/nodeinfo", apHandlers.NodeInfo)
		r.Get("/.well-known/host-meta", apHandlers.HostMeta)
		r.Get("/nodeinfo/2.0", apHandlers.NodeInfo20)

		// Shared inbox
		r.Post("/inbox", apHandlers.PostSharedInbox)

		// User/Actor endpoints
		r.Route("/users/{username}", func(r chi.Router) {
			r.Get("/", apHandlers.GetActor)
			r.Get("/outbox", apHandlers.GetOutbox)
			r.Get("/inbox", apHandlers.GetInbox)
			r.Post("/inbox", apHandlers.PostInbox)
			r.Get("/followers", apHandlers.GetFollowers)
			r.Get("/following", apHandlers.GetFollowing)
		})
	}

	// Additional API routes for videos and users (if they exist)
	r.Route("/api/v1", func(r chi.Router) {
		// Initialize views handler early for use in routes
		viewsHandler := video.NewViewsHandler(deps.ViewsService)

		r.Route("/videos", func(r chi.Router) {
			log.Printf("Registering video routes...")
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/", video.ListVideosHandler(deps.VideoRepo))
			// Static routes must come before parameterized routes
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/search", video.SearchVideosHandler(deps.VideoRepo))
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/qualities", video.GetSupportedQualities)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/top", viewsHandler.GetTopVideos)
			// Legacy one-shot upload endpoint for Postman collection compatibility
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/upload", video.UploadVideoFileHandler(deps.VideoRepo, cfg))
			// Parameterized routes come after static routes
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}", video.GetVideoHandler(deps.VideoRepo, deps.CaptionService))
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}/stream", video.StreamVideoHandler(deps.VideoRepo))
			// Subscription feed
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/subscriptions", channel.ListSubscriptionVideosHandler(deps.SubRepo))

			r.With(middleware.Auth(cfg.JWTSecret)).Post("/", video.CreateVideoHandler(deps.VideoRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/{id}", video.UpdateVideoHandler(deps.VideoRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{id}", video.DeleteVideoHandler(deps.VideoRepo))

			// Direct video upload endpoints (for backward compatibility with tests)
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/upload", video.VideoUploadChunkHandler(deps.UploadService, cfg))
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/complete", video.VideoCompleteUploadHandler(deps.UploadService))

			// Views and analytics endpoints for specific videos
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Post("/{id}/views", viewsHandler.TrackView)
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/{id}/analytics", viewsHandler.GetVideoAnalytics)
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/{id}/stats/daily", viewsHandler.GetDailyStats)

			// Comment endpoints
			commentHandlers := social.NewCommentHandlers(deps.CommentService)
			r.Route("/{videoId}/comments", func(r chi.Router) {
				r.Get("/", commentHandlers.GetComments)
				r.With(middleware.Auth(cfg.JWTSecret)).Post("/", commentHandlers.CreateComment)
			})

			// Rating endpoints
			ratingHandlers := social.NewRatingHandlers(deps.RatingService)
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/{id}/rating", ratingHandlers.SetRating)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}/rating", ratingHandlers.GetRating)
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{id}/rating", ratingHandlers.RemoveRating)

			// Watch Later shortcut
			playlistHandlers := social.NewPlaylistHandlers(deps.PlaylistService)
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/watch-later", playlistHandlers.AddToWatchLater)

			// Encoding jobs for a specific video
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/{id}/encoding-jobs", video.GetEncodingJobsByVideoHandler(deps.EncodingRepo, deps.VideoRepo))

			// Caption endpoints
			captionHandlers := social.NewCaptionHandlers(deps.CaptionService, deps.VideoRepo)
			r.Route("/{id}/captions", func(r chi.Router) {
				r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/", captionHandlers.GetCaptions)
				r.With(middleware.Auth(cfg.JWTSecret)).Post("/", captionHandlers.CreateCaption)
				r.Route("/{captionId}", func(r chi.Router) {
					r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/content", captionHandlers.GetCaptionContent)
					r.With(middleware.Auth(cfg.JWTSecret)).Put("/", captionHandlers.UpdateCaption)
					r.With(middleware.Auth(cfg.JWTSecret)).Delete("/", captionHandlers.DeleteCaption)
				})
			})
		})

		// Static HLS handler with privacy gating and cache headers
		r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/hls/*", video.HLSHandler(deps.VideoRepo))

		// Video import endpoints
		if deps.ImportService != nil {
			log.Printf("Registering video import routes...")
			// Type assert deps.ImportService to importuc.Service
			importService, ok := deps.ImportService.(importuc.Service)
			if ok {
				r.Route("/videos/imports", func(r chi.Router) {
					r.Use(middleware.Auth(cfg.JWTSecret))
					importHandlers := video.NewImportHandlers(importService)
					// SECURITY FIX: Apply stricter rate limiting to prevent SSRF abuse at scale
					r.With(strictImportLimiter.Limit).Post("/", importHandlers.CreateImport)
					r.Get("/", importHandlers.ListImports)
					r.Get("/{id}", importHandlers.GetImport)
					r.Delete("/{id}", importHandlers.CancelImport)
				})
			}
		}

		// Chunked upload endpoints
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

			// Individual job access (requires authentication)
			r.Route("/jobs", func(r chi.Router) {
				r.Use(middleware.Auth(cfg.JWTSecret))
				r.Get("/{jobID}", video.GetEncodingJobHandler(deps.EncodingRepo, deps.VideoRepo))
			})

			// User's encoding jobs
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/my-jobs", video.GetMyEncodingJobsHandler(deps.EncodingRepo, deps.VideoRepo))
		})

		r.Route("/users", func(r chi.Router) {
			// Admin-style create user; requires admin role
			r.With(middleware.Auth(cfg.JWTSecret), middleware.RequireRole("admin")).Post("/", auth.CreateUserHandler(deps.UserRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me", auth.GetCurrentUserHandler(deps.UserRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/me", auth.UpdateCurrentUserHandler(deps.UserRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/me/avatar", authHandlers.UploadAvatar)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}/videos", video.GetUserVideosHandler(deps.VideoRepo))
			// Subscriptions
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/subscribe", channel.SubscribeToUserHandler(deps.SubRepo, deps.UserRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{id}/subscribe", channel.UnsubscribeFromUserHandler(deps.SubRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me/subscriptions", channel.ListMySubscriptionsHandler(deps.SubRepo))

			// User's channels
			channelHandlers := channel.NewChannelHandlers(deps.ChannelService, deps.SubRepo)
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me/channels", channelHandlers.GetMyChannels)

			// User's ratings
			ratingHandlers := social.NewRatingHandlers(deps.RatingService)
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me/ratings", ratingHandlers.GetUserRatings)

			// User's Watch Later playlist
			playlistHandlers := social.NewPlaylistHandlers(deps.PlaylistService)
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me/watch-later", playlistHandlers.GetWatchLater)
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

		// Trending endpoint
		r.Get("/trending", viewsHandler.GetTrendingVideos)

		// Fingerprinting for view deduplication
		r.Post("/views/fingerprint", viewsHandler.GenerateFingerprint)

		// Channels
		r.Route("/channels", func(r chi.Router) {
			channelHandlers := channel.NewChannelHandlers(deps.ChannelService, deps.SubRepo)

			// Public routes
			r.Get("/", channelHandlers.ListChannels)
			r.Get("/{id}", channelHandlers.GetChannel)
			r.Get("/{id}/videos", channelHandlers.GetChannelVideos)
			r.Get("/{id}/subscribers", channelHandlers.GetChannelSubscribers)

			// Authenticated routes
			r.Group(func(r chi.Router) {
				r.Use(middleware.Auth(cfg.JWTSecret))
				r.Post("/", channelHandlers.CreateChannel)
				r.Put("/{id}", channelHandlers.UpdateChannel)
				r.Delete("/{id}", channelHandlers.DeleteChannel)
				r.Post("/{id}/subscribe", channelHandlers.SubscribeToChannel)
				r.Delete("/{id}/subscribe", channelHandlers.UnsubscribeFromChannel)
			})
		})

		// Live streams
		if deps.LiveStreamRepo != nil && deps.StreamKeyRepo != nil && deps.ViewerSessionRepo != nil {
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

				// HLS handlers (if transcoder is available)
				var hlsHandlers *video.HLSHandlers
				if deps.HLSTranscoder != nil {
					hlsHandlers = video.NewHLSHandlers(cfg, deps.LiveStreamRepo, deps.HLSTranscoder, deps.IPFSStreamingService)
				}

				// Authenticated routes
				r.Group(func(r chi.Router) {
					r.Use(middleware.Auth(cfg.JWTSecret))
					r.Post("/", liveStreamHandlers.CreateStream)
					r.Get("/active", liveStreamHandlers.GetActiveStreams)
				})

				// Channel-specific routes
				r.Route("/channels/{channelId}", func(r chi.Router) {
					r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/", liveStreamHandlers.ListChannelStreams)
				})

				// Stream-specific routes
				r.Route("/{id}", func(r chi.Router) {
					r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/", liveStreamHandlers.GetStream)
					r.With(middleware.Auth(cfg.JWTSecret)).Put("/", liveStreamHandlers.UpdateStream)
					r.With(middleware.Auth(cfg.JWTSecret)).Post("/end", liveStreamHandlers.EndStream)
					r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/stats", liveStreamHandlers.GetStreamStats)
					r.With(middleware.Auth(cfg.JWTSecret)).Post("/rotate-key", liveStreamHandlers.RotateStreamKey)

					// HLS endpoints (if transcoder is available)
					if hlsHandlers != nil {
						r.Route("/hls", func(r chi.Router) {
							r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/master.m3u8", hlsHandlers.GetMasterPlaylist)
							r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{variant}/index.m3u8", hlsHandlers.GetVariantPlaylist)
							r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{variant}/{segment}", hlsHandlers.GetSegment)
						})
						// HLS info endpoint
						r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/hls-info", hlsHandlers.GetStreamHLSInfo)
					}
				})
			})
		}

		// Comments (standalone endpoints)
		r.Route("/comments", func(r chi.Router) {
			commentHandlers := social.NewCommentHandlers(deps.CommentService)
			r.Get("/{commentId}", commentHandlers.GetComment)
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/{commentId}", commentHandlers.UpdateComment)
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{commentId}", commentHandlers.DeleteComment)
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{commentId}/flag", commentHandlers.FlagComment)
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{commentId}/flag", commentHandlers.UnflagComment)
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{commentId}/moderate", commentHandlers.ModerateComment)
		})

		// Notifications
		r.Route("/notifications", func(r chi.Router) {
			r.Use(middleware.Auth(cfg.JWTSecret))
			notificationHandlers := messaging.NewNotificationHandlers(deps.NotificationService)
			r.Get("/", notificationHandlers.GetNotifications)
			r.Get("/unread-count", notificationHandlers.GetUnreadCount)
			r.Get("/stats", notificationHandlers.GetNotificationStats)
			r.Put("/{id}/read", notificationHandlers.MarkAsRead)
			r.Put("/read-all", notificationHandlers.MarkAllAsRead)
			r.Delete("/{id}", notificationHandlers.DeleteNotification)
		})

		// IOTA Payment endpoints (if payment service is enabled)
		if cfg.EnableIOTA && deps.PaymentService != nil {
			log.Printf("Registering IOTA payment routes...")
			r.Route("/payments", func(r chi.Router) {
				r.Use(middleware.Auth(cfg.JWTSecret))
				paymentHandler := payments.NewPaymentHandler(deps.PaymentService)
				// Wallet management
				r.Post("/wallet", paymentHandler.CreateWallet)
				r.Get("/wallet", paymentHandler.GetWallet)
				// Payment intents
				r.Post("/intents", paymentHandler.CreatePaymentIntent)
				r.Get("/intents/{id}", paymentHandler.GetPaymentIntent)
				// Transaction history
				r.Get("/transactions", paymentHandler.GetTransactionHistory)
			})
		}

		// IPFS Streaming Metrics
		if deps.IPFSStreamingService != nil {
			r.Route("/ipfs", func(r chi.Router) {
				ipfsMetricsHandlers := video.NewIPFSMetricsHandlers(deps.IPFSStreamingService)
				r.Get("/metrics", ipfsMetricsHandlers.GetMetrics)
				r.Get("/gateways", ipfsMetricsHandlers.GetGatewayHealth)
			})
		}

		// Federation endpoints (ATProto)
		r.Route("/federation", func(r chi.Router) {
			fedHandlers := federation.NewFederationHandlers(deps.FederationRepo)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/timeline", fedHandlers.GetTimeline)
		})

		// Playlists
		r.Route("/playlists", func(r chi.Router) {
			playlistHandlers := social.NewPlaylistHandlers(deps.PlaylistService)

			// Public routes
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/", playlistHandlers.ListPlaylists)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}", playlistHandlers.GetPlaylist)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}/items", playlistHandlers.GetPlaylistItems)

			// Authenticated routes
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/", playlistHandlers.CreatePlaylist)
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/{id}", playlistHandlers.UpdatePlaylist)
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{id}", playlistHandlers.DeletePlaylist)
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/items", playlistHandlers.AddVideoToPlaylist)
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{id}/items/{itemId}", playlistHandlers.RemoveVideoFromPlaylist)
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/{id}/items/{itemId}/reorder", playlistHandlers.ReorderPlaylistItem)
		})

		// Moderation handlers
		moderationHandlers := moderation.NewModerationHandlers(deps.ModerationRepo)
		instanceHandlers := admin.NewInstanceHandlers(deps.ModerationRepo, deps.UserRepo, deps.VideoRepo)

		// Abuse reports - any authenticated user can create, admins/mods can manage
		r.Route("/abuse-reports", func(r chi.Router) {
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/", moderationHandlers.CreateAbuseReport)
		})

		// Admin moderation endpoints
		r.Route("/admin", func(r chi.Router) {
			r.Use(middleware.Auth(cfg.JWTSecret))
			r.Use(middleware.RequireRole(string(domain.RoleAdmin), string(domain.RoleMod)))

			// Abuse reports management
			r.Route("/abuse-reports", func(r chi.Router) {
				r.Get("/", moderationHandlers.ListAbuseReports)
				r.Get("/{id}", moderationHandlers.GetAbuseReport)
				r.Put("/{id}", moderationHandlers.UpdateAbuseReport)
				r.Delete("/{id}", moderationHandlers.DeleteAbuseReport)
			})

			// Blocklist management
			r.Route("/blocklist", func(r chi.Router) {
				r.Post("/", moderationHandlers.CreateBlocklistEntry)
				r.Get("/", moderationHandlers.ListBlocklistEntries)
				r.Put("/{id}", moderationHandlers.UpdateBlocklistEntry)
				r.Delete("/{id}", moderationHandlers.DeleteBlocklistEntry)
			})

			// Views management
			r.Route("/views", func(r chi.Router) {
				r.Use(middleware.RequireRole("admin"))
				r.Post("/aggregate", viewsHandler.AdminAggregateStats)
				r.Post("/cleanup", viewsHandler.AdminCleanupOldData)
			})

			// Instance configuration (admin only)
			r.Route("/instance/config", func(r chi.Router) {
				r.Use(middleware.RequireRole("admin"))
				r.Get("/", instanceHandlers.ListInstanceConfigs)
				r.Get("/{key}", instanceHandlers.GetInstanceConfig)
				r.Put("/{key}", instanceHandlers.UpdateInstanceConfig)
			})

			// OAuth client management (admin only)
			r.Route("/oauth/clients", func(r chi.Router) {
				r.Use(middleware.RequireRole("admin"))
				r.Get("/", authHandlers.AdminListOAuthClients)
				r.Post("/", authHandlers.AdminCreateOAuthClient)
				r.Put("/{clientId}/secret", authHandlers.AdminRotateOAuthClientSecret)
				r.Delete("/{clientId}", authHandlers.AdminDeleteOAuthClient)
			})

			// Federation jobs (admin)
			fedAdminHandlers := federation.NewAdminFederationHandlers(deps.FederationRepo)
			r.Route("/federation/jobs", func(r chi.Router) {
				r.Get("/", fedAdminHandlers.ListJobs)
				r.Get("/{id}", fedAdminHandlers.GetJob)
				r.Post("/{id}/retry", fedAdminHandlers.RetryJob)
				r.Delete("/{id}", fedAdminHandlers.DeleteJob)
			})

			// Federation actors (admin)
			fedActorsHandlers := federation.NewAdminFederationActorsHandlers(deps.FederationRepo)
			r.Route("/federation/actors", func(r chi.Router) {
				r.Get("/", fedActorsHandlers.ListActors)
				r.Post("/", fedActorsHandlers.UpsertActor)
				r.Put("/{actor}", fedActorsHandlers.UpdateActor)
				r.Delete("/{actor}", fedActorsHandlers.DeleteActor)
			})

			// Federation hardening (admin)
			fh := federation.NewFederationHardeningHandler(deps.HardeningService)
			r.Route("/federation/hardening", func(r chi.Router) {
				// Dashboard and health
				r.Get("/dashboard", fh.GetDashboard)
				r.Get("/health", fh.GetHealthMetrics)
				// DLQ
				r.Get("/dlq", fh.GetDLQJobs)
				r.Post("/dlq/{id}/retry", fh.RetryDLQJob)
				// Blocklists
				r.Route("/blocklist", func(r chi.Router) {
					r.Get("/instances", fh.GetInstanceBlocks)
					r.Post("/instances", fh.BlockInstance)
					r.Delete("/instances/{domain}", fh.UnblockInstance)
					r.Post("/actors", fh.BlockActor)
					r.Get("/check", fh.CheckBlocked)
				})
				// Abuse workflows
				r.Route("/abuse", func(r chi.Router) {
					r.Get("/reports", fh.GetAbuseReports)
					r.Post("/reports/{id}/resolve", fh.ResolveAbuseReport)
				})
				// Cleanup
				r.Post("/cleanup", fh.RunCleanup)
			})
		})

		// Public instance information
		r.Route("/instance", func(r chi.Router) {
			r.Get("/about", instanceHandlers.GetInstanceAbout)
		})
	})

	// OEmbed endpoint (outside of /api/v1)
	r.Get("/oembed", admin.NewInstanceHandlers(deps.ModerationRepo, deps.UserRepo, deps.VideoRepo).OEmbed)

	// ATProto well-known DID endpoint for handle verification
	r.Get("/.well-known/atproto-did", admin.NewInstanceHandlers(deps.ModerationRepo, deps.UserRepo, deps.VideoRepo).WellKnownAtprotoDID)

	// Custom 404 handler that returns JSON error response
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("NOT_FOUND %s %s", r.Method, r.URL.Path)
		shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("NOT_FOUND", "The requested resource was not found"))
	})

	// Custom 405 handler for method not allowed
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		shared.WriteError(w, http.StatusMethodNotAllowed, domain.NewDomainError("METHOD_NOT_ALLOWED", "Method not allowed for this endpoint"))
	})

	// Debug: log all registered routes when log level is debug/trace
	if lvl := strings.ToLower(cfg.LogLevel); lvl == "debug" || lvl == "trace" {
		_ = chi.Walk(r, func(method string, route string, handler http.Handler, middlewares ...func(http.Handler) http.Handler) error {
			log.Printf("ROUTE %s %s", method, route)
			return nil
		})
	}
}
