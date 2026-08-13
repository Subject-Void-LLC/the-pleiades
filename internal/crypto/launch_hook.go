package crypto

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/hook"
)

// This file gives SavedLaunchConfig.answers the same envelope encryption
// Device.properties has, as the thin adapter encryptPropertiesMap's own doc
// comment says a second entity should be: the map-level logic, which is
// where this package's hard-won correctness lives, is shared rather than
// copied.
//
// Survey answers need it because a survey is the one path by which a
// launching operator supplies a value the template author did not write,
// and AWX's own survey types include `password`. A saved configuration
// therefore holds credentials: a vault token, a sudo password, an API key
// somebody typed into a launch form and asked the platform to remember.
//
// The WHOLE map is encrypted rather than only the password-typed entries.
// Encrypting selectively would mean this hook had to know which questions
// are passwords, which lives on the template's survey, one join away and
// unavailable inside a mutation hook. Encrypting everything is strictly
// safer, cannot be got wrong per row, and costs nothing anybody needs: no
// query filters on an answer value.
//
// Encryption at rest and redaction on the wire are separate controls for
// separate exposures, and one does not cover the other. This is the first;
// the API's own projection, which replaces a password answer with a marker
// rather than its value, is the second.

// SavedLaunchConfigAnswersHook encrypts SavedLaunchConfig.answers on write.
//
// Registered for OpCreate, OpUpdate and OpUpdateOne, matching the Device
// hook: answers are not Immutable in the schema, because a saved
// configuration is a thing an operator edits, and every builder that can
// reach the column has to pass through the same interception point. Which
// builder a future caller happens to choose is not what should decide
// whether a credential is encrypted.
func SavedLaunchConfigAnswersHook(svc *EnvelopeService) ent.Hook {
	return hook.On(
		func(next ent.Mutator) ent.Mutator {
			return hook.SavedLaunchConfigFunc(func(ctx context.Context, m *ent.SavedLaunchConfigMutation) (ent.Value, error) {
				answers, exists := m.Answers()
				if exists && answers != nil {
					encrypted, err := encryptPropertiesMap(svc, answers)
					if err != nil {
						return nil, fmt.Errorf("failed to encrypt survey answers: %w", err)
					}
					m.SetAnswers(encrypted)
				}
				return next.Mutate(ctx, m)
			})
		},
		ent.OpCreate|ent.OpUpdate|ent.OpUpdateOne,
	)
}

// SavedLaunchConfigAnswersInterceptor decrypts answers on read.
//
// A decrypt failure is logged and that row's answers are left in their
// raw, still-encrypted shape rather than aborting the query, for the reason
// the Device interceptor states at length: results arrive as a slice even
// for Only and Get, so hard-failing on the first bad row would break every
// healthy row in the same query. Here that would mean one unreadable saved
// configuration making a template's whole list of them unloadable.
func SavedLaunchConfigAnswersInterceptor(svc *EnvelopeService) ent.Interceptor {
	return ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
			v, err := next.Query(ctx, q)
			if err != nil {
				return nil, err
			}

			result, ok := v.([]*ent.SavedLaunchConfig)
			if !ok {
				return v, nil
			}

			for _, cfg := range result {
				if cfg.Answers == nil {
					continue
				}
				decrypted, err := decryptPropertiesMap(svc, cfg.Answers)
				if err != nil {
					slog.Warn("failed to decrypt survey answers, leaving encrypted",
						slog.Int("saved_launch_config", cfg.ID), slog.String("error", err.Error()))
					continue
				}
				cfg.Answers = decrypted
			}

			return v, nil
		})
	})
}
