package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	"github.com/argoproj/argo-events/pkg/apis/events/v1alpha1"
)

func TestMergeTemplate(t *testing.T) {
	defaults := &v1alpha1.Template{
		Metadata:           &v1alpha1.Metadata{Labels: map[string]string{"team": "platform", "env": "prod"}},
		ServiceAccountName: "default-sa",
		SecurityContext:    &corev1.PodSecurityContext{RunAsNonRoot: ptr.To(true), RunAsUser: ptr.To(int64(1000))},
		Volumes:            []corev1.Volume{{Name: "a"}, {Name: "b"}},
		Tolerations:        []corev1.Toleration{{Key: "default"}},
	}

	t.Run("no defaults", func(t *testing.T) {
		tmpl := &v1alpha1.Template{ServiceAccountName: "sa"}
		merged, err := MergeTemplate(nil, tmpl)
		assert.NoError(t, err)
		assert.Equal(t, tmpl, merged)
	})

	t.Run("no template", func(t *testing.T) {
		merged, err := MergeTemplate(defaults, nil)
		assert.NoError(t, err)
		assert.Equal(t, defaults, merged)
	})

	t.Run("template takes precedence and is merged", func(t *testing.T) {
		tmpl := &v1alpha1.Template{
			Metadata:           &v1alpha1.Metadata{Labels: map[string]string{"env": "dev"}},
			ServiceAccountName: "my-sa",
			SecurityContext:    &corev1.PodSecurityContext{FSGroup: ptr.To(int64(2000)), RunAsUser: ptr.To(int64(3000))},
			Volumes:            []corev1.Volume{{Name: "b", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}, {Name: "c"}},
			Tolerations:        []corev1.Toleration{{Key: "mine"}},
		}
		merged, err := MergeTemplate(defaults, tmpl)
		assert.NoError(t, err)
		// scalars: template wins
		assert.Equal(t, "my-sa", merged.ServiceAccountName)
		// maps: merged, template wins on conflicts
		assert.Equal(t, map[string]string{"team": "platform", "env": "dev"}, merged.Metadata.Labels)
		// structs: merged field by field
		assert.Equal(t, &corev1.PodSecurityContext{RunAsNonRoot: ptr.To(true), RunAsUser: ptr.To(int64(3000)), FSGroup: ptr.To(int64(2000))}, merged.SecurityContext)
		// lists with a merge key: merged by name
		assert.Len(t, merged.Volumes, 3)
		// lists without a merge key: replaced
		assert.Equal(t, []corev1.Toleration{{Key: "mine"}}, merged.Tolerations)
		// inputs are not modified
		assert.Equal(t, "default-sa", defaults.ServiceAccountName)
		assert.Equal(t, map[string]string{"env": "dev"}, tmpl.Metadata.Labels)
	})
}
