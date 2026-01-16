package resources

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/redpanda-data/common-go/kube"
	clusterv1 "github.com/redpanda-data/common-go/kube/example/api/cluster/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type lockedStorage struct {
	data            map[types.NamespacedName]*Virtual
	resourceVersion atomic.Uint64
}

func (s *lockedStorage) upsert(resource *Virtual) {
	if found, ok := s.data[client.ObjectKeyFromObject(resource)]; ok {
		resource.UID = found.UID
	} else {
		resource.UID = uuid.NewUUID()
	}
	resource.ResourceVersion = strconv.Itoa(int(s.resourceVersion.Add(1)))
	s.data[client.ObjectKeyFromObject(resource)] = resource
}

func (s *lockedStorage) list(namespace string) []Virtual {
	resources := []Virtual{}
	for _, resource := range s.data {
		if namespace == metav1.NamespaceAll || namespace == resource.Namespace {
			resources = append(resources, *resource)
		}
	}

	slices.SortStableFunc(resources, func(a Virtual, b Virtual) int {
		if v := cmp.Compare(a.Namespace, b.Namespace); v != 0 {
			return v
		}
		return cmp.Compare(a.Name, b.Name)
	})

	return resources
}

func (s *lockedStorage) delete(key types.NamespacedName) *Virtual {
	resource := s.data[key]
	delete(s.data, key)
	return resource
}

func (s *lockedStorage) get(key types.NamespacedName) *Virtual {
	return s.data[key]
}

func (s *lockedStorage) markLinked(key types.NamespacedName) {
	for _, virtual := range s.list(key.Namespace) {
		if virtual.Spec.ClusterRef.Name == key.Name {
			virtual.Status.Linked = true
			s.upsert(&virtual)
		}
	}
}

func (s *lockedStorage) markUnlinked(key types.NamespacedName) {
	for _, virtual := range s.list(key.Namespace) {
		if virtual.Spec.ClusterRef.Name == key.Name {
			virtual.Status.Linked = false
			s.upsert(&virtual)
		}
	}
}

func newLockedStorage() *lockedStorage {
	return &lockedStorage{
		data: make(map[types.NamespacedName]*Virtual),
	}
}

type VirtualStorage struct {
	ctl           *kube.Ctl
	lockedStorage *lockedStorage
	lock          sync.RWMutex
}

func NewVirtualStorage(ctl *kube.Ctl) *VirtualStorage {
	return &VirtualStorage{
		ctl:           ctl,
		lockedStorage: newLockedStorage(),
	}
}

var _ rest.Storage = (*VirtualStorage)(nil)
var _ rest.Getter = (*VirtualStorage)(nil)
var _ rest.Lister = (*VirtualStorage)(nil)
var _ rest.Scoper = (*VirtualStorage)(nil)
var _ rest.Creater = (*VirtualStorage)(nil)
var _ rest.Updater = (*VirtualStorage)(nil)
var _ rest.GracefulDeleter = (*VirtualStorage)(nil)
var _ rest.SingularNameProvider = (*VirtualStorage)(nil)

func (s *VirtualStorage) Destroy() {}

func (s *VirtualStorage) NamespaceScoped() bool {
	return true
}

func (v *VirtualStorage) GetSingularName() string {
	return "virtual"
}

func (s *VirtualStorage) ConvertToTable(ctx context.Context, object runtime.Object, tableOptions runtime.Object) (*metav1.Table, error) {
	table := &metav1.Table{
		ColumnDefinitions: []metav1.TableColumnDefinition{
			{Name: "Name", Type: "string", Format: "name", Description: "Name of the Virtual"},
			{Name: "Namespace", Type: "string", Format: "namespace", Description: "Namespace of the Virtual"},
			{Name: "Linked", Type: "boolean", Format: "linked", Description: "Whether Virtual is linked or not to its referenced cluster"},
		},
	}

	switch v := object.(type) {
	case *VirtualList:
		for _, item := range v.Items {
			row := metav1.TableRow{
				Object: runtime.RawExtension{Object: &item},
				Cells:  []any{item.Name, item.Namespace, item.Status.Linked},
			}
			table.Rows = append(table.Rows, row)
		}
	case *Virtual:
		row := metav1.TableRow{
			Object: runtime.RawExtension{Object: v},
			Cells:  []any{v.Name, v.Namespace, v.Status.Linked},
		}
		table.Rows = append(table.Rows, row)
	default:
		return nil, errors.New("unknown type")
	}

	return table, nil
}

func (s *VirtualStorage) New() runtime.Object {
	return &Virtual{}
}

func (s *VirtualStorage) NewList() runtime.Object {
	return &VirtualList{}
}

func (s *VirtualStorage) Get(ctx context.Context, name string, options *metav1.GetOptions) (runtime.Object, error) {
	namespace, ok := apirequest.NamespaceFrom(ctx)
	if !ok {
		namespace = metav1.NamespaceDefault
	}
	key := types.NamespacedName{Namespace: namespace, Name: name}

	s.lock.RLock()
	defer s.lock.RUnlock()

	if resource := s.lockedStorage.get(key); resource != nil {
		return resource, nil
	}

	return nil, apierrors.NewNotFound(GroupVersion.WithResource("virtual").GroupResource(), namespace+"/"+name)
}

func (s *VirtualStorage) Create(ctx context.Context, object runtime.Object, createValidation rest.ValidateObjectFunc, options *metav1.CreateOptions) (runtime.Object, error) {
	s.lock.Lock()
	defer s.lock.Unlock()

	virtual := object.(*Virtual)

	if err := s.upsertVirtual(ctx, virtual); err != nil {
		return nil, err
	}

	return virtual, nil
}

func (s *VirtualStorage) Delete(ctx context.Context, name string, deleteValidation rest.ValidateObjectFunc, options *metav1.DeleteOptions) (runtime.Object, bool, error) {
	namespace, ok := apirequest.NamespaceFrom(ctx)
	if !ok {
		namespace = metav1.NamespaceDefault
	}
	key := types.NamespacedName{Namespace: namespace, Name: name}

	s.lock.Lock()
	defer s.lock.Unlock()

	r := s.lockedStorage.delete(key)
	if r == nil {
		return nil, false, apierrors.NewNotFound(GroupVersion.WithResource("virtual").GroupResource(), name)
	}

	return r, true, nil
}

func (s *VirtualStorage) Update(ctx context.Context, name string, objInfo rest.UpdatedObjectInfo, createValidation rest.ValidateObjectFunc, updateValidation rest.ValidateObjectUpdateFunc, forceAllowCreate bool, options *metav1.UpdateOptions) (runtime.Object, bool, error) {
	namespace, ok := apirequest.NamespaceFrom(ctx)
	if !ok {
		namespace = metav1.NamespaceDefault
	}
	key := types.NamespacedName{Namespace: namespace, Name: name}

	s.lock.Lock()
	defer s.lock.Unlock()

	existing := s.lockedStorage.get(key)
	if existing == nil {
		return nil, false, apierrors.NewNotFound(GroupVersion.WithResource("virtual").GroupResource(), namespace+"/"+name)
	}

	updated, err := objInfo.UpdatedObject(ctx, existing)
	if err != nil {
		return nil, false, err
	}

	virtual := updated.(*Virtual)

	if err := s.upsertVirtual(ctx, virtual); err != nil {
		return nil, false, err
	}

	return virtual, false, nil
}

func (s *VirtualStorage) List(ctx context.Context, options *metainternalversion.ListOptions) (runtime.Object, error) {
	namespace, ok := apirequest.NamespaceFrom(ctx)
	if !ok {
		namespace = metav1.NamespaceDefault
	}

	s.lock.RLock()
	defer s.lock.RUnlock()

	return &VirtualList{
		Items: s.lockedStorage.list(namespace),
	}, nil
}

func (s *VirtualStorage) MarkLinked(key types.NamespacedName) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.lockedStorage.markLinked(key)
}

func (s *VirtualStorage) MarkUnlinked(key types.NamespacedName) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.lockedStorage.markUnlinked(key)
}

func (s *VirtualStorage) checkCluster(ctx context.Context, key types.NamespacedName) (bool, error) {
	_, err := kube.Get[clusterv1.Cluster](ctx, s.ctl, key)
	if err != nil && !apierrors.IsNotFound(err) {
		return false, err
	}
	return err == nil, nil
}

func (s *VirtualStorage) upsertVirtual(ctx context.Context, virtual *Virtual) error {
	clusterName := virtual.Spec.ClusterRef.Name
	virtual.Status.Linked = false
	if clusterName != "" {
		found, err := s.checkCluster(ctx, types.NamespacedName{Namespace: virtual.Namespace, Name: clusterName})
		if err != nil {
			return err
		}

		virtual.Status.Linked = found
	}
	s.lockedStorage.upsert(virtual)
	return nil
}
