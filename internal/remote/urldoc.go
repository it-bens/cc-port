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
  s3://bucket?region=us-east-1
      With --credentials-file ~/.config/cc-port/aws.env: a separate
      .env-style file holds AWS_ACCESS_KEY_ID and
      AWS_SECRET_ACCESS_KEY, keeping secrets out of the URL and out
      of process env.

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
  cc-port resolves credentials in this order before handing them to
  the SDK:
    1. --credentials-file <path> (.env-style, AWS_* keys, mode 0600)
    2. AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY env vars
    3. Interactive TTY prompt for any required field still missing
       (suppressed by --no-prompt; hard error when no TTY)
  When --credentials-file is set, the file's fields take precedence
  over env on conflicts; env fills any field the file does not
  supply. With none of these set, falls through to the AWS SDK
  default chain: ~/.aws/credentials (?profile=<name>), IAM role on
  EC2, IMDS, ECS task role.

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
