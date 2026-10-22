package app

// Checkout preparation state. The order row is durable before the channel call;
// the channel result is then recorded explicitly so a missing QR is never
// indistinguishable from a normal pending payment.
import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
)

func systemCardReady(ctx context.Context, q commerceQuery, jid string) (bool, error) {
	var exists bool
	err := q.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM job_images
		WHERE job_id=$1 AND kind IN ('system_card','system')
		  AND COALESCE(file,'')<>''
	)`, jid).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return exists, err
}

func isDefinitiveWxError(err error) bool {
	var apiErr *wxAPIError
	return errors.As(err, &apiErr)
}

// Only a stable, non-secret identifier is persisted. Full provider bodies and
// credentials must never enter the order row or application logs.
func safeWxError(err error) string {
	var apiErr *wxAPIError
	if errors.As(err, &apiErr) {
		return "wx:" + apiErr.Code
	}
	return "wx:network_or_signature"
}

func markCheckoutFailed(id string, err error) {
	code := safeWxError(err)
	slog.Error("wx native checkout failed", "order", id, "error", code)
	_, _ = pool.Exec(contextBG(), `UPDATE orders
		SET status='failed', checkout_state='failed', checkout_error=$2, updated_at=now()
		WHERE id=$1 AND status IN ('pending','expired')`, id, code)
}

func markCheckoutUnknown(id string, err error) {
	code := safeWxError(err)
	slog.Warn("wx native checkout outcome unknown", "order", id, "error", code)
	_, _ = pool.Exec(contextBG(), `UPDATE orders
		SET checkout_state='unknown', checkout_error=$2, updated_at=now()
		WHERE id=$1 AND status IN ('pending','expired')`, id, code)
}
