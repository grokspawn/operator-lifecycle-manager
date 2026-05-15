package catalog

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient/operatorclientmocks"
)

func TestMergedOwnerReferences(t *testing.T) {
	var (
		True  = true
		False = false
	)

	for _, tc := range []struct {
		Name string
		In   [][]metav1.OwnerReference
		Out  []metav1.OwnerReference
	}{
		{
			Name: "empty",
		},
		{
			Name: "different uid",
			In: [][]metav1.OwnerReference{
				{
					{
						APIVersion:         "a",
						Kind:               "b",
						Name:               "c",
						Controller:         &True,
						BlockOwnerDeletion: &True,
						UID:                "x",
					},
					{
						APIVersion:         "a",
						Kind:               "b",
						Name:               "c",
						Controller:         &True,
						BlockOwnerDeletion: &True,
						UID:                "y",
					},
				},
			},
			Out: []metav1.OwnerReference{
				{
					APIVersion:         "a",
					Kind:               "b",
					Name:               "c",
					Controller:         &True,
					BlockOwnerDeletion: &True,
					UID:                "x",
				},
				{
					APIVersion:         "a",
					Kind:               "b",
					Name:               "c",
					Controller:         &True,
					BlockOwnerDeletion: &True,
					UID:                "y",
				},
			},
		},
		{
			Name: "different controller",
			In: [][]metav1.OwnerReference{
				{
					{
						APIVersion:         "a",
						Kind:               "b",
						Name:               "c",
						Controller:         &True,
						BlockOwnerDeletion: &True,
						UID:                "x",
					},
					{
						APIVersion:         "a",
						Kind:               "b",
						Name:               "c",
						Controller:         &False,
						BlockOwnerDeletion: &True,
						UID:                "x",
					},
				},
			},
			Out: []metav1.OwnerReference{
				{
					APIVersion:         "a",
					Kind:               "b",
					Name:               "c",
					Controller:         &True,
					BlockOwnerDeletion: &True,
					UID:                "x",
				},
				{
					APIVersion:         "a",
					Kind:               "b",
					Name:               "c",
					Controller:         &False,
					BlockOwnerDeletion: &True,
					UID:                "x",
				},
			},
		},
		{
			Name: "add owner without uid",
			In: [][]metav1.OwnerReference{
				{
					{
						APIVersion:         "a",
						Kind:               "b",
						Name:               "c-1",
						Controller:         &False,
						BlockOwnerDeletion: &False,
						UID:                "x",
					},
				},
				{
					{
						APIVersion:         "a",
						Kind:               "b",
						Name:               "c-2",
						Controller:         &False,
						BlockOwnerDeletion: &False,
						UID:                "",
					},
				},
			},
			Out: []metav1.OwnerReference{
				{
					APIVersion:         "a",
					Kind:               "b",
					Name:               "c-1",
					Controller:         &False,
					BlockOwnerDeletion: &False,
					UID:                "x",
				},
				{
					APIVersion:         "a",
					Kind:               "b",
					Name:               "c-2",
					Controller:         &False,
					BlockOwnerDeletion: &False,
					UID:                "",
				},
			},
		},
		{
			Name: "duplicates combined",
			In: [][]metav1.OwnerReference{
				{
					{
						APIVersion:         "a",
						Kind:               "b",
						Name:               "c",
						Controller:         &False,
						BlockOwnerDeletion: &False,
						UID:                "x",
					},
				},
				{
					{
						APIVersion:         "a",
						Kind:               "b",
						Name:               "c",
						Controller:         &False,
						BlockOwnerDeletion: &False,
						UID:                "x",
					},
				},
			},
			Out: []metav1.OwnerReference{
				{
					APIVersion:         "a",
					Kind:               "b",
					Name:               "c",
					Controller:         &False,
					BlockOwnerDeletion: &False,
					UID:                "x",
				},
			},
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			assert.ElementsMatch(t, tc.Out, mergedOwnerReferences(tc.In...))
		})
	}
}

func TestEnsureServiceAccount(t *testing.T) {
	namespace := "test-namespace"
	saName := "test-sa"

	tests := []struct {
		name                     string
		existingServiceAccount   *corev1.ServiceAccount
		existingSecrets          []runtime.Object
		newServiceAccount        *corev1.ServiceAccount
		expectedAnnotations      map[string]string
		expectedStatus           v1alpha1.StepStatus
		expectError              bool
		createError              error
		getError                 error
		updateError              error
		verifyTokenSecretCreated bool
	}{
		{
			name: "create new service account",
			newServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
					Annotations: map[string]string{
						"new-annotation": "new-value",
					},
				},
			},
			expectedAnnotations: map[string]string{
				"new-annotation": "new-value",
			},
			expectedStatus: v1alpha1.StepStatusCreated,
		},
		{
			name: "update existing service account - preserve existing annotations",
			existingServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
					Annotations: map[string]string{
						"existing-annotation": "existing-value",
						"override-annotation": "old-value",
					},
				},
				Secrets: []corev1.ObjectReference{
					{Name: "existing-secret"},
				},
			},
			newServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
					Annotations: map[string]string{
						"new-annotation":      "new-value",
						"override-annotation": "new-value",
					},
				},
			},
			expectedAnnotations: map[string]string{
				"existing-annotation": "existing-value",
				"new-annotation":      "new-value",
				"override-annotation": "new-value",
			},
			expectedStatus: v1alpha1.StepStatusPresent,
			createError:    apierrors.NewAlreadyExists(corev1.Resource("serviceaccounts"), saName),
		},
		{
			name: "update existing service account - no annotations on new SA",
			existingServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
					Annotations: map[string]string{
						"existing-annotation": "existing-value",
					},
				},
			},
			newServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
				},
			},
			expectedAnnotations: map[string]string{
				"existing-annotation": "existing-value",
			},
			expectedStatus: v1alpha1.StepStatusPresent,
			createError:    apierrors.NewAlreadyExists(corev1.Resource("serviceaccounts"), saName),
		},
		{
			name: "update existing service account - preserve secrets",
			existingServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
				},
				Secrets: []corev1.ObjectReference{
					{Name: "secret-1"},
					{Name: "secret-2"},
				},
			},
			newServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
				},
			},
			expectedAnnotations: map[string]string{},
			expectedStatus:      v1alpha1.StepStatusPresent,
			createError:         apierrors.NewAlreadyExists(corev1.Resource("serviceaccounts"), saName),
		},
		{
			name: "create error - not already exists",
			newServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
				},
			},
			createError: apierrors.NewInternalError(assert.AnError),
			expectError: true,
		},
		{
			name: "update error - get existing fails",
			existingServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
				},
			},
			newServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
				},
			},
			createError: apierrors.NewAlreadyExists(corev1.Resource("serviceaccounts"), saName),
			getError:    apierrors.NewInternalError(assert.AnError),
			expectError: true,
		},
		{
			name: "create new service account - includes token secret creation",
			newServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
				},
			},
			expectedAnnotations:      map[string]string{},
			expectedStatus:           v1alpha1.StepStatusCreated,
			verifyTokenSecretCreated: true,
		},
		{
			name: "update existing service account - token secret already exists",
			existingServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
				},
				Secrets: []corev1.ObjectReference{
					{Name: saName + "-token"},
				},
			},
			existingSecrets: []runtime.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      saName + "-token",
						Namespace: namespace,
						Annotations: map[string]string{
							corev1.ServiceAccountNameKey: saName,
						},
						Labels: map[string]string{
							"olm.managed": "true",
						},
					},
					Type: corev1.SecretTypeServiceAccountToken,
				},
			},
			newServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
					Annotations: map[string]string{
						"new-annotation": "value",
					},
				},
			},
			expectedAnnotations:      map[string]string{"new-annotation": "value"},
			expectedStatus:           v1alpha1.StepStatusPresent,
			createError:              apierrors.NewAlreadyExists(corev1.Resource("serviceaccounts"), saName),
			verifyTokenSecretCreated: true, // Verifies it's not duplicated
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			// Create mock client
			mockClient := operatorclientmocks.NewMockClientInterface(ctrl)

			// Create fake kubernetes client
			var objects []runtime.Object
			if tc.existingServiceAccount != nil {
				objects = append(objects, tc.existingServiceAccount)
			}
			if tc.existingSecrets != nil {
				objects = append(objects, tc.existingSecrets...)
			}

			//nolint:staticcheck // SA1019: NewClientset not available until apply configurations are generated
			fakeClient := k8sfake.NewSimpleClientset(objects...)

			// Setup expectations
			mockClient.EXPECT().KubernetesInterface().Return(fakeClient).AnyTimes()

			// Mock the create call
			if tc.createError != nil {
				// We need to intercept the create call and return the error
				fakeClient.PrependReactor("create", "serviceaccounts", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, tc.createError
				})
			}

			// Mock the get call if needed
			if tc.getError != nil {
				fakeClient.PrependReactor("get", "serviceaccounts", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, tc.getError
				})
			}

			// Mock UpdateServiceAccount if the test expects an update
			if tc.createError != nil && apierrors.IsAlreadyExists(tc.createError) && tc.getError == nil {
				// Calculate expected SA after merge
				expectedSA := tc.newServiceAccount.DeepCopy()
				if tc.existingServiceAccount != nil {
					expectedSA.Secrets = tc.existingServiceAccount.Secrets
					// Merge annotations
					if expectedSA.Annotations == nil {
						expectedSA.Annotations = make(map[string]string)
					}
					for k, v := range tc.existingServiceAccount.Annotations {
						if _, ok := expectedSA.Annotations[k]; !ok {
							expectedSA.Annotations[k] = v
						}
					}
				}
				expectedSA.SetNamespace(namespace)

				mockClient.EXPECT().UpdateServiceAccount(gomock.Any()).DoAndReturn(func(sa *corev1.ServiceAccount) (*corev1.ServiceAccount, error) {
					// Verify the merged service account has the expected annotations
					assert.Equal(t, tc.expectedAnnotations, sa.Annotations)
					// Verify secrets were preserved if they existed
					if tc.existingServiceAccount != nil && len(tc.existingServiceAccount.Secrets) > 0 {
						assert.Equal(t, tc.existingServiceAccount.Secrets, sa.Secrets)
					}
					return sa, tc.updateError
				}).MaxTimes(1)
			}

			// Create StepEnsurer
			ensurer := &StepEnsurer{
				kubeClient: mockClient,
				//nolint:staticcheck // SA1019: NewClientset not available until apply configurations are generated
				crClient:      fake.NewSimpleClientset(),
				dynamicClient: fakedynamic.NewSimpleDynamicClient(runtime.NewScheme()),
			}

			// Execute EnsureServiceAccount
			status, err := ensurer.EnsureServiceAccount(namespace, tc.newServiceAccount)

			// Verify results
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.expectedStatus, status)

				// Verify token secret creation if requested
				if tc.verifyTokenSecretCreated {
					secretName := saName + "-token"
					secret, err := fakeClient.CoreV1().Secrets(namespace).Get(
						context.TODO(), secretName, metav1.GetOptions{},
					)
					require.NoError(t, err, "token secret should have been created")

					// Verify secret type
					assert.Equal(t, corev1.SecretTypeServiceAccountToken, secret.Type)

					// Verify annotation linking to ServiceAccount
					assert.Equal(t, saName, secret.Annotations[corev1.ServiceAccountNameKey])

					// Verify OLM managed label
					assert.Equal(t, "true", secret.Labels["olm.managed"])

					// Verify no duplicate secrets were created
					secrets, err := fakeClient.CoreV1().Secrets(namespace).List(
						context.TODO(), metav1.ListOptions{},
					)
					require.NoError(t, err)

					tokenSecretCount := 0
					for _, s := range secrets.Items {
						if s.Name == secretName && s.Type == corev1.SecretTypeServiceAccountToken {
							tokenSecretCount++
						}
					}

					assert.Equal(t, 1, tokenSecretCount, "should have exactly one token secret, not duplicates")
				}
			}
		})
	}
}

func TestEnsureServiceAccountTokenSecret(t *testing.T) {
	namespace := "test-namespace"
	saName := "test-sa"

	tests := []struct {
		name                  string
		serviceAccount        *corev1.ServiceAccount
		existingSecrets       []runtime.Object
		expectSecretCreation  bool
		expectError           bool
		expectedErrorContains string
		secretGetError        error
		secretCreateError     error
	}{
		{
			name: "creates token secret for new service account with no secrets",
			serviceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
				},
			},
			expectSecretCreation: true,
		},
		{
			name: "skips creation when token secret already exists in SA.Secrets",
			serviceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
				},
				Secrets: []corev1.ObjectReference{
					{Name: saName + "-token"},
				},
			},
			existingSecrets: []runtime.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      saName + "-token",
						Namespace: namespace,
					},
					Type: corev1.SecretTypeServiceAccountToken,
				},
			},
			expectSecretCreation: false,
		},
		{
			name: "creates token when only non-token secrets exist",
			serviceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
				},
				Secrets: []corev1.ObjectReference{
					{Name: "some-other-secret"},
				},
			},
			existingSecrets: []runtime.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "some-other-secret",
						Namespace: namespace,
					},
					Type: corev1.SecretTypeOpaque,
				},
			},
			expectSecretCreation: true,
		},
		{
			name: "creates token when multiple non-token secrets exist",
			serviceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
				},
				Secrets: []corev1.ObjectReference{
					{Name: "secret-1"},
					{Name: "secret-2"},
				},
			},
			existingSecrets: []runtime.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "secret-1",
						Namespace: namespace,
					},
					Type: corev1.SecretTypeOpaque,
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "secret-2",
						Namespace: namespace,
					},
					Type: corev1.SecretTypeDockerConfigJson,
				},
			},
			expectSecretCreation: true,
		},
		{
			name: "skips creation when one of multiple secrets is a token",
			serviceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
				},
				Secrets: []corev1.ObjectReference{
					{Name: "secret-1"},
					{Name: saName + "-token"},
				},
			},
			existingSecrets: []runtime.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "secret-1",
						Namespace: namespace,
					},
					Type: corev1.SecretTypeOpaque,
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      saName + "-token",
						Namespace: namespace,
					},
					Type: corev1.SecretTypeServiceAccountToken,
				},
			},
			expectSecretCreation: false,
		},
		{
			name: "reuses orphaned token secret instead of creating duplicate",
			serviceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
				},
			},
			existingSecrets: []runtime.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      saName + "-token",
						Namespace: namespace,
					},
					Type: corev1.SecretTypeServiceAccountToken,
				},
			},
			expectSecretCreation: false,
		},
		{
			name: "creates token when orphaned secret exists but is not token type",
			serviceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
				},
			},
			existingSecrets: []runtime.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      saName + "-token",
						Namespace: namespace,
					},
					Type: corev1.SecretTypeOpaque,
				},
			},
			expectSecretCreation: true,
		},
		{
			name: "propagates secret creation error",
			serviceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
				},
			},
			secretCreateError:     apierrors.NewInternalError(assert.AnError),
			expectError:           true,
			expectedErrorContains: "Internal error",
		},
		{
			name: "propagates secret get error when checking referenced secrets",
			serviceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
				},
				Secrets: []corev1.ObjectReference{
					{Name: "some-secret"},
				},
			},
			secretGetError:        apierrors.NewInternalError(assert.AnError),
			expectError:           true,
			expectedErrorContains: "Internal error",
		},
		{
			name: "handles NotFound error when checking referenced secrets",
			serviceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
				},
				Secrets: []corev1.ObjectReference{
					{Name: "missing-secret"},
				},
			},
			expectSecretCreation: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockClient := operatorclientmocks.NewMockClientInterface(ctrl)

			objects := append([]runtime.Object{tc.serviceAccount}, tc.existingSecrets...)
			//nolint:staticcheck // SA1019: NewClientset not available until apply configurations are generated
			fakeClient := k8sfake.NewSimpleClientset(objects...)

			mockClient.EXPECT().KubernetesInterface().Return(fakeClient).AnyTimes()

			// Set up reactors for error simulation
			if tc.secretCreateError != nil {
				fakeClient.PrependReactor("create", "secrets", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, tc.secretCreateError
				})
			}

			if tc.secretGetError != nil {
				fakeClient.PrependReactor("get", "secrets", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, tc.secretGetError
				})
			}

			ensurer := &StepEnsurer{
				kubeClient: mockClient,
				//nolint:staticcheck // SA1019: NewClientset not available until apply configurations are generated
				crClient:      fake.NewSimpleClientset(),
				dynamicClient: fakedynamic.NewSimpleDynamicClient(runtime.NewScheme()),
			}

			err := ensurer.ensureServiceAccountTokenSecret(namespace, tc.serviceAccount)

			if tc.expectError {
				require.Error(t, err)
				if tc.expectedErrorContains != "" {
					assert.Contains(t, err.Error(), tc.expectedErrorContains)
				}
			} else {
				require.NoError(t, err)

				if tc.expectSecretCreation {
					// Verify secret was created with correct structure
					secretName := saName + "-token"
					secret, err := fakeClient.CoreV1().Secrets(namespace).Get(
						context.TODO(), secretName, metav1.GetOptions{},
					)
					require.NoError(t, err)

					// Verify secret type
					assert.Equal(t, corev1.SecretTypeServiceAccountToken, secret.Type)

					// Verify annotation linking to ServiceAccount
					assert.Equal(t, saName, secret.Annotations[corev1.ServiceAccountNameKey])

					// Verify OLM managed label
					assert.Equal(t, "true", secret.Labels["olm.managed"])

					// Verify secret name follows pattern
					assert.Equal(t, secretName, secret.Name)
				} else {
					// Verify no duplicate secret was created
					secrets, err := fakeClient.CoreV1().Secrets(namespace).List(
						context.TODO(), metav1.ListOptions{},
					)
					require.NoError(t, err)

					// Count token secrets with the expected name
					tokenSecretCount := 0
					for _, s := range secrets.Items {
						if s.Name == saName+"-token" && s.Type == corev1.SecretTypeServiceAccountToken {
							tokenSecretCount++
						}
					}

					// Should be exactly 1 (the existing one), not 2 (duplicate)
					assert.LessOrEqual(t, tokenSecretCount, 1, "should not create duplicate token secret")
				}
			}
		})
	}
}
