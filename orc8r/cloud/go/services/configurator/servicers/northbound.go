/*
Copyright 2020 The Magma Authors.

This source code is licensed under the BSD-style license found in the
LICENSE file in the root directory of this source tree.

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package servicers

import (
	"context"
	"fmt"

	"magma/orc8r/cloud/go/serde"
	"magma/orc8r/cloud/go/services/configurator"
	"magma/orc8r/cloud/go/services/configurator/protos"
	"magma/orc8r/cloud/go/services/configurator/storage"
	orc8rStorage "magma/orc8r/cloud/go/storage"
	commonProtos "magma/orc8r/lib/go/protos"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type nbConfiguratorServicer struct {
	factory storage.ConfiguratorStorageFactory
}

// NewNorthboundConfiguratorServicer returns a configurator server backed by storage passed in
func NewNorthboundConfiguratorServicer(factory storage.ConfiguratorStorageFactory) (protos.NorthboundConfiguratorServer, error) {
	if factory == nil {
		return nil, fmt.Errorf("Storage factory is nil")
	}
	return &nbConfiguratorServicer{factory}, nil
}

func (srv *nbConfiguratorServicer) LoadNetworks(context context.Context, req *protos.LoadNetworksRequest) (*storage.NetworkLoadResult, error) {
	res := &storage.NetworkLoadResult{}
	store, err := srv.factory.StartTransaction(context, &orc8rStorage.TxOptions{ReadOnly: true})
	if err != nil {
		return res, err
	}

	result, err := store.LoadNetworks(*req.Filter, *req.Criteria)
	if err != nil {
		storage.RollbackLogOnError(store)
		return res, err
	}
	return &result, store.Commit()
}

func (srv *nbConfiguratorServicer) ListNetworkIDs(context context.Context, void *commonProtos.Void) (*protos.ListNetworkIDsResponse, error) {
	res := &protos.ListNetworkIDsResponse{}
	store, err := srv.factory.StartTransaction(context, &orc8rStorage.TxOptions{ReadOnly: true})
	if err != nil {
		return res, err
	}

	networks, err := store.LoadAllNetworks(storage.FullNetworkLoadCriteria)
	if err != nil {
		storage.RollbackLogOnError(store)
		return res, err
	}
	res.NetworkIDs = []string{}
	for _, network := range networks {
		res.NetworkIDs = append(res.NetworkIDs, network.ID)
	}
	return res, store.Commit()
}

func (srv *nbConfiguratorServicer) CreateNetworks(context context.Context, req *protos.CreateNetworksRequest) (*protos.CreateNetworksResponse, error) {
	emptyRes := &protos.CreateNetworksResponse{}
	store, err := srv.factory.StartTransaction(context, &orc8rStorage.TxOptions{ReadOnly: false})
	if err != nil {
		return emptyRes, err
	}

	createdNetworks := make([]*storage.Network, 0, len(req.Networks))
	for _, network := range req.Networks {
		err = networkConfigsAreValid(network.Configs)
		if err != nil {
			return emptyRes, err
		}
		createdNetwork, err := store.CreateNetwork(*network)
		if err != nil {
			storage.RollbackLogOnError(store)
			return emptyRes, err
		}
		createdNetworks = append(createdNetworks, &createdNetwork)
	}
	return &protos.CreateNetworksResponse{CreatedNetworks: createdNetworks}, store.Commit()
}

func (srv *nbConfiguratorServicer) UpdateNetworks(context context.Context, req *protos.UpdateNetworksRequest) (*commonProtos.Void, error) {
	void := &commonProtos.Void{}
	store, err := srv.factory.StartTransaction(context, &orc8rStorage.TxOptions{ReadOnly: false})
	if err != nil {
		return void, err
	}

	updates := []storage.NetworkUpdateCriteria{}
	for _, update := range req.Updates {
		err = networkConfigsAreValid(update.ConfigsToAddOrUpdate)
		if err != nil {
			return void, err
		}
		updates = append(updates, *update)
	}
	err = store.UpdateNetworks(updates)
	if err != nil {
		storage.RollbackLogOnError(store)
		return void, err
	}
	return void, store.Commit()
}

func (srv *nbConfiguratorServicer) DeleteNetworks(context context.Context, req *protos.DeleteNetworksRequest) (*commonProtos.Void, error) {
	void := &commonProtos.Void{}
	store, err := srv.factory.StartTransaction(context, &orc8rStorage.TxOptions{ReadOnly: false})
	if err != nil {
		return void, err
	}

	deleteRequests := []storage.NetworkUpdateCriteria{}
	for _, networkID := range req.NetworkIDs {
		deleteRequests = append(deleteRequests, storage.NetworkUpdateCriteria{ID: networkID, DeleteNetwork: true})
	}
	err = store.UpdateNetworks(deleteRequests)
	if err != nil {
		storage.RollbackLogOnError(store)
		return void, err
	}
	return void, store.Commit()
}

func (srv *nbConfiguratorServicer) LoadEntities(context context.Context, req *protos.LoadEntitiesRequest) (*storage.EntityLoadResult, error) {
	emptyRes := &storage.EntityLoadResult{}
	store, err := srv.factory.StartTransaction(context, &orc8rStorage.TxOptions{ReadOnly: false})
	if err != nil {
		return emptyRes, err
	}

	loadResult, err := store.LoadEntities(req.NetworkID, *req.Filter, *req.Criteria)
	if err != nil {
		storage.RollbackLogOnError(store)
		return emptyRes, err
	}
	return &loadResult, store.Commit()
}

func (srv *nbConfiguratorServicer) WriteEntities(context context.Context, req *protos.WriteEntitiesRequest) (*protos.WriteEntitiesResponse, error) {
	emptyRes := &protos.WriteEntitiesResponse{}
	store, err := srv.factory.StartTransaction(context, &orc8rStorage.TxOptions{ReadOnly: false})
	if err != nil {
		return emptyRes, err
	}

	ret := &protos.WriteEntitiesResponse{
		UpdatedEntities: map[string]*storage.NetworkEntity{},
	}
	for _, write := range req.Writes {
		switch op := write.Request.(type) {
		case *protos.WriteEntityRequest_Create:
			createdEnt, err := createEntity(store, req.NetworkID, op.Create)
			if err != nil {
				storage.RollbackLogOnError(store)
				return emptyRes, status.Error(codes.Internal, err.Error())
			}
			ret.CreatedEntities = append(ret.CreatedEntities, createdEnt)
		case *protos.WriteEntityRequest_Update:
			updatedEnt, err := updateEntity(store, req.NetworkID, op.Update)
			if err != nil {
				storage.RollbackLogOnError(store)
				return emptyRes, status.Error(codes.Internal, err.Error())
			}
			ret.UpdatedEntities[updatedEnt.Key] = updatedEnt
		default:
			storage.RollbackLogOnError(store)
			return emptyRes, status.Error(codes.InvalidArgument, fmt.Sprintf("write request %T not recognized", write))
		}
	}
	return ret, store.Commit()
}

func (srv *nbConfiguratorServicer) CreateEntities(context context.Context, req *protos.CreateEntitiesRequest) (*protos.CreateEntitiesResponse, error) {
	emptyRes := &protos.CreateEntitiesResponse{}
	store, err := srv.factory.StartTransaction(context, &orc8rStorage.TxOptions{ReadOnly: false})
	if err != nil {
		return emptyRes, err
	}

	createdEntities := []*storage.NetworkEntity{}
	for _, entity := range req.Entities {
		createdEntity, err := createEntity(store, req.NetworkID, entity)
		if err != nil {
			storage.RollbackLogOnError(store)
			return emptyRes, err
		}
		createdEntities = append(createdEntities, createdEntity)
	}
	return &protos.CreateEntitiesResponse{CreatedEntities: createdEntities}, store.Commit()
}

func (srv *nbConfiguratorServicer) UpdateEntities(context context.Context, req *protos.UpdateEntitiesRequest) (*protos.UpdateEntitiesResponse, error) {
	emptyRes := &protos.UpdateEntitiesResponse{}
	store, err := srv.factory.StartTransaction(context, &orc8rStorage.TxOptions{ReadOnly: false})
	if err != nil {
		return emptyRes, err
	}

	updatedEntities := map[string]*storage.NetworkEntity{}
	for _, update := range req.Updates {
		updatedEntity, err := updateEntity(store, req.NetworkID, update)
		if err != nil {
			storage.RollbackLogOnError(store)
			return emptyRes, err
		}
		updatedEntities[update.Key] = updatedEntity
	}
	return &protos.UpdateEntitiesResponse{UpdatedEntities: updatedEntities}, store.Commit()
}

func (srv *nbConfiguratorServicer) DeleteEntities(context context.Context, req *protos.DeleteEntitiesRequest) (*commonProtos.Void, error) {
	void := &commonProtos.Void{}
	store, err := srv.factory.StartTransaction(context, &orc8rStorage.TxOptions{ReadOnly: false})
	if err != nil {
		return void, err
	}

	for _, entityID := range req.ID {
		request := storage.EntityUpdateCriteria{
			Type:         entityID.Type,
			Key:          entityID.Key,
			DeleteEntity: true,
		}
		_, err = store.UpdateEntity(req.NetworkID, request)
		if err != nil {
			storage.RollbackLogOnError(store)
			return void, err
		}
	}
	return void, store.Commit()
}

func networkConfigsAreValid(configs map[string][]byte) error {
	for typeVal, config := range configs {
		_, err := serde.DeserializeLegacy(configurator.NetworkConfigSerdeDomain, typeVal, config)
		if err != nil {
			return err
		}
	}
	return nil
}

func entityConfigIsValid(typeVal string, config []byte) error {
	_, err := serde.DeserializeLegacy(configurator.NetworkEntitySerdeDomain, typeVal, config)
	if err != nil {
		return err
	}
	return nil
}

func createEntity(store storage.ConfiguratorStorage, networkID string, entity *storage.NetworkEntity) (*storage.NetworkEntity, error) {
	if err := entityConfigIsValid(entity.Type, entity.Config); err != nil {
		return nil, err
	}
	ent, err := store.CreateEntity(networkID, *entity)
	return &ent, err
}

func updateEntity(store storage.ConfiguratorStorage, networkID string, update *storage.EntityUpdateCriteria) (*storage.NetworkEntity, error) {
	if update.NewConfig != nil {
		if err := entityConfigIsValid(update.Type, update.NewConfig.Value); err != nil {
			return nil, err
		}
	}

	updatedEntity, err := store.UpdateEntity(networkID, *update)
	if err != nil {
		return nil, err
	}
	return &updatedEntity, nil
}
