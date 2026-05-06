package credentials

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

// resolveWith is the testable seam under Resolve. The prompter argument
// is the production osTTYPrompter for the public Resolve and a fake for
// tests. A nil prompter is valid when opts.Prompt is false.
func resolveWith(ctx context.Context, opts ResolveOptions, prompter ttyPrompter) (aws.CredentialsProvider, error) {
	var (
		merged       credentialFields
		triedSources []string
		anyContrib   bool
	)

	if opts.Path != "" {
		fileFields, err := parseFile(opts.Path)
		if err != nil {
			return nil, err
		}
		merged = mergePreferLeft(merged, fileFields)
		triedSources = append(triedSources, "file")
		if !isZero(fileFields) {
			anyContrib = true
		}
	}

	envFields := readEnv()
	merged = mergePreferLeft(merged, envFields)
	if !isZero(envFields) {
		triedSources = append(triedSources, "env")
		anyContrib = true
	}

	missing := missingRequiredFields(merged)
	if len(missing) == 0 {
		if !anyContrib {
			return nil, nil
		}
		return staticProvider(merged), nil
	}

	if !anyContrib {
		return nil, nil
	}

	if !opts.Prompt {
		return nil, &IncompleteCredentialsError{MissingFields: missing, TriedSources: triedSources}
	}

	for _, field := range missing {
		value, err := prompter.Prompt(ctx, field)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, fmt.Errorf("canceled: %w", ctxErr)
			}
			return nil, fmt.Errorf("prompt %s: %w", field, err)
		}
		switch field {
		case envKeyAccessKeyID:
			merged.accessKeyID = value
		case envKeySecretAccessKey:
			merged.secretAccessKey = value
		case envKeySessionToken:
			merged.sessionToken = value
		}
	}
	triedSources = append(triedSources, "prompt")

	if remaining := missingRequiredFields(merged); len(remaining) > 0 {
		return nil, &IncompleteCredentialsError{MissingFields: remaining, TriedSources: triedSources}
	}
	return staticProvider(merged), nil
}

// mergePreferLeft returns left with any zero field replaced by the
// corresponding field from right. Earlier source wins on conflict;
// later source fills gaps.
func mergePreferLeft(left, right credentialFields) credentialFields {
	if left.accessKeyID == "" {
		left.accessKeyID = right.accessKeyID
	}
	if left.secretAccessKey == "" {
		left.secretAccessKey = right.secretAccessKey
	}
	if left.sessionToken == "" {
		left.sessionToken = right.sessionToken
	}
	return left
}

func isZero(fields credentialFields) bool {
	return fields.accessKeyID == "" && fields.secretAccessKey == "" && fields.sessionToken == ""
}

// missingRequiredFields returns the names of required fields not yet
// populated. Session token is optional and never appears in the result.
// Order is stable: AKID before secret.
func missingRequiredFields(fields credentialFields) []string {
	var missing []string
	if fields.accessKeyID == "" {
		missing = append(missing, envKeyAccessKeyID)
	}
	if fields.secretAccessKey == "" {
		missing = append(missing, envKeySecretAccessKey)
	}
	return missing
}

func staticProvider(fields credentialFields) aws.CredentialsProvider {
	return credentials.NewStaticCredentialsProvider(fields.accessKeyID, fields.secretAccessKey, fields.sessionToken)
}
