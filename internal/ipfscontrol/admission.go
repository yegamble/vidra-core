package ipfscontrol

import "context"

// Admission reads the actual node observation without inventing free capacity.
// Legacy/external installations are not implicitly enrolled by this method.
func (s *Service) Admission(ctx context.Context) (Document, HostStatus, error) {
	doc, err := s.Config(ctx)
	if err != nil {
		return doc, HostStatus{}, err
	}
	if s.host == nil {
		return doc, HostStatus{}, ErrManagerUnavailable
	}
	host, err := s.host.Status(ctx)
	if err == nil {
		err = host.Validate()
	}
	return doc, host, err
}
