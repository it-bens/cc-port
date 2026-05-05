package remote

// URLDoc is the curated --help block describing remote URLs accepted by
// New. The push and pull cobra commands append this verbatim to their
// Long text. Authoritative parameter reference: "go doc
// gocloud.dev/blob/s3blob.URLOpener" and "go doc
// gocloud.dev/aws.V2ConfigFromURLParams".
const URLDoc = `Remote URL formats:

Local filesystem
  file:///absolute/path/to/dir
      Any local directory: synced drive (iCloud, Dropbox), mounted
      share (NFS, SMB), external disk.

AWS S3
  s3://bucket?region=us-east-1
      Plain bucket. Credentials come from the AWS SDK chain: env
      vars, ~/.aws/credentials, or an attached IAM role.
  s3://bucket/team-a?region=us-east-1
      Push or pull under a key prefix instead of the bucket root.
  s3://bucket?region=us-east-1&profile=archive
      Pick a non-default ~/.aws/credentials profile.
  s3://bucket?region=us-east-1&anonymous=true
      Anonymous access for a public bucket; no credentials sent.
  s3://bucket?region=us-east-1&ssetype=aws:kms&kmskeyid=<key-arn>
      Server-side encryption with a customer-managed KMS key.
      ssetype values: AES256, aws:kms, aws:kms:dsse.
  s3://bucket?region=us-east-1&accelerate=true
      Use S3 Transfer Acceleration endpoints.

S3-compatible (Cloudflare R2, Backblaze B2, MinIO, Ceph, DigitalOcean
Spaces, Wasabi, Hetzner Object Storage, etc.)
  Substitute the endpoint and region your provider issues. With any
  custom endpoint, also set hostname_immutable=true so the SDK does
  not rewrite the host.

  s3://bucket?region=<region>&endpoint=<host>&hostname_immutable=true
      Hosted S3-compatible service over HTTPS, virtual-hosted style.
  s3://bucket?region=us-east-1&endpoint=<host>:<port>&hostname_immutable=true&use_path_style=true&disable_https=true
      Self-hosted endpoint over plain HTTP, path-style addressing.

Authentication
  Every s3:// URL uses the AWS SDK credential chain. Set
  AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY (or the access keys
  your provider issues), or pick a profile via ?profile=<name>, or
  attach an IAM role. cc-port reads no credentials of its own.

Query parameter reference
  region              AWS region or provider equivalent.
  endpoint            Custom S3 endpoint host (non-AWS only).
  hostname_immutable  "true" with a custom endpoint; keeps the SDK
                      from rewriting the host.
  use_path_style      "true" for path-style addressing. Required by
                      most self-hosted setups.
  disable_https       "true" for plain-HTTP endpoints.
  profile             Shared-config profile name.
  anonymous           "true" forces anonymous credentials.
  ssetype, kmskeyid   Server-side encryption parameters.
  accelerate          "true" for S3 Transfer Acceleration.

  Full list: "go doc gocloud.dev/blob/s3blob.URLOpener" and "go doc
  gocloud.dev/aws.V2ConfigFromURLParams".`
