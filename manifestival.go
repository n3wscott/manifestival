package manifestival

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

var (
	log = logf.Log.WithName("manifestival")
)

type Manifest interface {
	// Either updates or creates all resources in the manifest
	ApplyAll() error
	// Updates or creates a particular resource
	Apply(*unstructured.Unstructured) error
	// Deletes all resources in the manifest
	DeleteAll() error
	// Deletes a particular resource
	Delete(spec *unstructured.Unstructured) error
	// Retains every resource for which all FilterFn's return true
	Filter(fns ...FilterFn) Manifest
	// Returns a deep copy of the matching resource
	Find(apiVersion string, kind string, name string) *unstructured.Unstructured
	// Returns a deep copy of all resources in the manifest
	DeepCopyResources() []unstructured.Unstructured
	// Convenient list of all the resource names in the manifest
	ResourceNames() []string
}

type YamlManifest struct {
	client    client.Client
	resources []unstructured.Unstructured
}

var _ Manifest = &YamlManifest{}

func NewYamlManifest(pathname string, recursive bool, client client.Client) (Manifest, error) {
	log.Info("Reading YAML file", "name", pathname)
	resources, err := Parse(pathname, recursive)
	if err != nil {
		return nil, err
	}
	return &YamlManifest{resources: resources, client: client}, nil
}

func (f *YamlManifest) ApplyAll() error {
	for _, spec := range f.resources {
		if err := f.Apply(&spec); err != nil {
			return err
		}
	}
	return nil
}

func (f *YamlManifest) Apply(spec *unstructured.Unstructured) error {
	key := client.ObjectKey{Namespace: spec.GetNamespace(), Name: spec.GetName()}
	current := &unstructured.Unstructured{}
	current.SetGroupVersionKind(spec.GroupVersionKind())
	err := f.client.Get(context.TODO(), key, current)
	if err != nil {
		// Create new one
		if !errors.IsNotFound(err) {
			return err
		}
		log.Info("Creating", "type", spec.GroupVersionKind(), "name", spec.GetName())
		if err = f.client.Create(context.TODO(), spec); err != nil {
			return err
		}
	} else {
		// Update existing one
		log.Info("Updating", "type", spec.GroupVersionKind(), "name", spec.GetName())
		// We need to preserve the current content, specifically
		// 'metadata.resourceVersion' and 'spec.clusterIP', so we
		// only overwrite fields set in our resource
		content := current.UnstructuredContent()
		for k, v := range spec.UnstructuredContent() {
			if k == "metadata" || k == "spec" || k == "data" {
				m := v.(map[string]interface{})
				for kn, vn := range m {
					unstructured.SetNestedField(content, vn, k, kn)
				}
			} else {
				content[k] = v
			}
		}
		current.SetUnstructuredContent(content)
		if err = f.client.Update(context.TODO(), current); err != nil {
			return err
		}
	}
	return nil
}

func (f *YamlManifest) DeleteAll() error {
	a := make([]unstructured.Unstructured, len(f.resources))
	copy(a, f.resources)
	// we want to delete in reverse order
	for left, right := 0, len(a)-1; left < right; left, right = left+1, right-1 {
		a[left], a[right] = a[right], a[left]
	}
	for _, spec := range a {
		if err := f.Delete(&spec); err != nil {
			return err
		}
	}
	return nil
}

func (f *YamlManifest) Delete(spec *unstructured.Unstructured) error {
	key := client.ObjectKey{Namespace: spec.GetNamespace(), Name: spec.GetName()}
	current := &unstructured.Unstructured{}
	current.SetGroupVersionKind(spec.GroupVersionKind())
	if err := f.client.Get(context.TODO(), key, current); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
	}
	log.Info("Deleting", "type", spec.GroupVersionKind(), "name", spec.GetName())
	if err := f.client.Delete(context.TODO(), spec); err != nil {
		// ignore GC race conditions triggered by owner references
		if !errors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (f *YamlManifest) Find(apiVersion string, kind string, name string) *unstructured.Unstructured {
	for _, spec := range f.resources {
		if spec.GetAPIVersion() == apiVersion &&
			spec.GetKind() == kind &&
			spec.GetName() == name {
			return spec.DeepCopy()
		}
	}
	return nil
}

func (f *YamlManifest) DeepCopyResources() []unstructured.Unstructured {
	result := make([]unstructured.Unstructured, len(f.resources))
	for i, spec := range f.resources {
		result[i] = *spec.DeepCopy()
	}
	return result
}

func (f *YamlManifest) ResourceNames() []string {
	var names []string
	for _, spec := range f.resources {
		names = append(names, fmt.Sprintf("%s/%s (%s)", spec.GetNamespace(), spec.GetName(), spec.GroupVersionKind()))
	}
	return names
}