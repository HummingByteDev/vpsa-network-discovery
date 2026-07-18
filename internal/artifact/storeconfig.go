package artifact

import (
	"fmt"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/config"
)

// StoreFromConfig builds the artifact store from CNIP_ARTIFACT_* settings.
// It is deliberately provider-agnostic: any S3-compatible service works with
// the same five variables — switching providers is an endpoint change, never
// a code change.
//
//	CNIP_ARTIFACT_S3_ENDPOINT    host[:port], no scheme. Examples:
//	                               AWS S3          s3.eu-central-1.amazonaws.com
//	                               Cloudflare R2   <account-id>.r2.cloudflarestorage.com
//	                               Backblaze B2    s3.us-west-004.backblazeb2.com
//	                               minio (dev)     localhost:9000
//	CNIP_ARTIFACT_S3_ACCESS_KEY  access key / key ID / application key ID
//	CNIP_ARTIFACT_S3_SECRET_KEY  secret key / application key
//	CNIP_ARTIFACT_S3_BUCKET      bucket name (default cnip-artifacts)
//	CNIP_ARTIFACT_S3_REGION      optional; AWS requires it, R2 uses "auto",
//	                             B2/minio infer it
//	CNIP_ARTIFACT_S3_USE_SSL     default true (false only for local dev)
//
//	CNIP_ARTIFACT_DIR            filesystem store instead of S3 (tests,
//	                             single-host deployments)
//
// Returns (nil, nil) when nothing is configured; callers decide whether a
// store is mandatory.
func StoreFromConfig(cfg *config.Source) (Store, error) {
	endpoint := cfg.String("ARTIFACT_S3_ENDPOINT", "")
	dir := cfg.String("ARTIFACT_DIR", "")
	s3cfg := S3Config{
		Endpoint:  endpoint,
		AccessKey: cfg.String("ARTIFACT_S3_ACCESS_KEY", ""),
		SecretKey: cfg.String("ARTIFACT_S3_SECRET_KEY", ""),
		Bucket:    cfg.String("ARTIFACT_S3_BUCKET", "cnip-artifacts"),
		Region:    cfg.String("ARTIFACT_S3_REGION", ""),
		UseSSL:    cfg.Bool("ARTIFACT_S3_USE_SSL", true),
	}
	switch {
	case endpoint != "" && dir != "":
		return nil, fmt.Errorf("CNIP_ARTIFACT_S3_ENDPOINT and CNIP_ARTIFACT_DIR are mutually exclusive")
	case endpoint != "":
		return NewS3Store(s3cfg)
	case dir != "":
		return FSStore{Root: dir}, nil
	default:
		return nil, nil
	}
}
