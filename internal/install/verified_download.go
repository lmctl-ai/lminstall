package install

import "context"

// An availability fallback must retain the exact digest. Never retry integrity,
// TLS or HTTPS-policy failures against a different host.
func downloadMirrored(ctx context.Context, primary, mirror, dest, digest string) error {
	if err := download(ctx, primary, dest, digest); err != nil {
		if !downloadAvailabilityError(err) {
			return err
		}
		return download(ctx, mirror, dest, digest)
	}
	return nil
}
