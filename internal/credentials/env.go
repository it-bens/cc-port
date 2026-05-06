package credentials

import "os"

// readEnv reads the three AWS_* credential variables from the process
// environment. Empty values are treated as "not contributed."
func readEnv() credentialFields {
	return credentialFields{
		accessKeyID:     os.Getenv(envKeyAccessKeyID),
		secretAccessKey: os.Getenv(envKeySecretAccessKey),
		sessionToken:    os.Getenv(envKeySessionToken),
	}
}
