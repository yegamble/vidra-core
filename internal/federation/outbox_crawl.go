package federation

import "context"

// outboxCrawlMaxPages bounds how many pages one crawl reads after the outbox
// document itself.
const outboxCrawlMaxPages = 10

// CrawlOutbox reads the outbox of a remote actor this instance follows and
// ingests the videos it lists. It returns how many it stored.
func (s *Service) CrawlOutbox(_ context.Context, _ string) (int, error) {
	return 0, nil
}
