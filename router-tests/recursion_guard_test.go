package integration

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wundergraph/cosmo/router-tests/testenv"
	"github.com/wundergraph/cosmo/router/pkg/config"
)

func guard(max int, ignoreAPQ bool) func(*config.SecurityConfiguration) {
	return func(sc *config.SecurityConfiguration) {
		if sc.ComplexityLimits == nil {
			sc.ComplexityLimits = &config.ComplexityLimits{}
		}
		sc.ComplexityLimits.RecursionGuard = &config.RecursionGuardConfig{
			Enabled:                   true,
			MaxDepth:                  max,
			IgnorePersistedOperations: ignoreAPQ,
		}
	}
}

const shallowQuery = `
	query ShallowEmployees {
		employees {
			id
			products           # enum values only
			hobbies {
				__typename
				employees {      # depth 2
					id
				}
			}
		}
	}`

const deepQuery = `
	query DeepEmployees {
		employees {
			id
			products
			hobbies {
				__typename
				employees {
					id
					hobbies {
						__typename
						employees {
							id
							hobbies {
								__typename
								employees {       # depth 4 -> should overflow when maxDepth = 3
									id
								}
							}
						}
					}
				}
			}
		}
	}`

func TestRecursionGuard(t *testing.T) {
	t.Parallel()

	t.Run("query_with_valid_recursion_depth_should_succeed", func(t *testing.T) {
		t.Parallel()

		testenv.Run(t, &testenv.Config{
			ModifySecurityConfiguration: guard(3, false),
		}, func(t *testing.T, env *testenv.Environment) {
			res := env.MakeGraphQLRequestOK(testenv.GraphQLRequest{Query: shallowQuery})
			require.NotContains(t, res.Body, "recursion")
		})
	})

	t.Run("query_with_excessive_recursive_depth_should_fail", func(t *testing.T) {
		t.Parallel()

		testenv.Run(t, &testenv.Config{
			ModifySecurityConfiguration: guard(3, false),
		}, func(t *testing.T, env *testenv.Environment) {
			out, err := env.MakeGraphQLRequest(testenv.GraphQLRequest{Query: deepQuery})
			require.NoError(t, err)
			require.Equal(t, 400, out.Response.StatusCode)

			var body map[string]any
			require.NoError(t, json.Unmarshal([]byte(out.Body), &body))

			errs, ok := body["errors"].([]any)
			require.True(t, ok && len(errs) > 0, "router returned no GraphQL errors")

			msg, _ := errs[0].(map[string]any)["message"].(string)
			require.Contains(t, strings.ToLower(msg), "recursion")
		})
	})

	t.Run("persisted_operation_with_recursion_should_succeed_when_ignore_persisted_operations_is_true",
		func(t *testing.T) {
			t.Parallel()

			testenv.Run(t, &testenv.Config{
				ModifySecurityConfiguration: guard(3, true),
			}, func(t *testing.T, env *testenv.Environment) {
				hash := "employees-recursion-hash"

				// 1) register APQ
				register := map[string]any{
					"query": deepQuery,
					"extensions": map[string]any{
						"persistedQuery": map[string]any{
							"version":    1,
							"sha256Hash": hash,
						},
					},
				}
				b, _ := json.Marshal(register)
				env.MakeRequest("POST", "/graphql",
					map[string][]string{"Content-Type": {"application/json"}},
					strings.NewReader(string(b)),
				)

				// 2) request by hash only
				op := map[string]any{
					"extensions": map[string]any{
						"persistedQuery": map[string]any{
							"version":    1,
							"sha256Hash": hash,
						},
					},
				}
				b, _ = json.Marshal(op)
				res, err := env.MakeRequest("POST", "/graphql",
					map[string][]string{"Content-Type": {"application/json"}},
					strings.NewReader(string(b)),
				)
				require.NoError(t, err)
				require.Equal(t, 200, res.StatusCode)
			})
		})
}
