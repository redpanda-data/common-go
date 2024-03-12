package pagination_test

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/redpanda-data/common-go/api/pagination"
)

func TestSliceToPaginated(t *testing.T) {
	type Topic struct {
		Name       string
		Partitions int32
	}

	createCollection := func(size int) []Topic {
		collection := make([]Topic, size)
		for i := range size {
			collection[i] = Topic{
				Name:       strconv.Itoa(i),
				Partitions: 3,
			}
		}
		return collection
	}

	tt := []struct {
		name           string
		collection     []Topic
		pageSize       int
		validationFunc func(t *testing.T, page []Topic, token string, err error)
	}{
		{
			name:       "sorted collection into multiple pages",
			collection: createCollection(12),
			pageSize:   5,
			validationFunc: func(t *testing.T, page []Topic, token string, err error) {
				require.NotNil(t, page)
				require.NoError(t, err)
				require.Len(t, page, 5)
				assert.NotEmpty(t, token)

				// Check if page contains expected items
				for i, item := range page {
					assert.Equal(t, strconv.Itoa(i), item.Name)
				}

				// Decode and verify the contents of the next page token
				keySetPage, err := pagination.DecodeToken(token, []string{"name"})
				require.NoError(t, err)
				assert.Equal(t, "5", keySetPage.ValueGreaterEqual)
			},
		},
		{
			name:       "collection is smaller than page size",
			collection: createCollection(20),
			pageSize:   25,
			validationFunc: func(t *testing.T, page []Topic, token string, err error) {
				require.NotNil(t, page)
				require.NoError(t, err)
				assert.Len(t, page, 20)
				assert.Empty(t, token)
			},
		},
		{
			name:       "collection is nil",
			collection: nil,
			pageSize:   25,
			validationFunc: func(t *testing.T, page []Topic, token string, err error) {
				assert.Nil(t, page)
				assert.NoError(t, err)
				assert.Empty(t, token)
			},
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			page, nextPageToken, err := pagination.SliceToPaginated(tc.collection, tc.pageSize, "name", func(x Topic) string {
				return x.Name
			})
			tc.validationFunc(t, page, nextPageToken, err)
		})
	}
}

func TestSliceToPaginatedWithToken(t *testing.T) {
	type Topic struct {
		Name       string
		Partitions int32
	}

	createCollection := func(size int) []Topic {
		collection := make([]Topic, size)
		for i := range size {
			collection[i] = Topic{
				Name:       strconv.Itoa(i),
				Partitions: 3,
			}
		}
		return collection
	}

	collection := createCollection(14)

	// 1. Get first 5 results
	page, nextPageToken, err := pagination.SliceToPaginatedWithToken(collection, 5, "", "name", func(x Topic) string {
		return x.Name
	})
	require.NoError(t, err)
	require.NotEmpty(t, nextPageToken)
	assert.NotNil(t, page)
	require.Len(t, page, 5)

	// Created topics are named by the index from 0-n, so we can use this
	// index to compare the expected topic name
	topicName := 0
	for _, topic := range page {
		assert.Equal(t, strconv.Itoa(topicName), topic.Name)
		topicName++
	}

	// 2. Get slice with the next results (6th-10th)
	page, nextPageToken, err = pagination.SliceToPaginatedWithToken(collection, 5, nextPageToken, "name", func(x Topic) string {
		return x.Name
	})
	require.NoError(t, err)
	require.NotEmpty(t, nextPageToken)
	assert.NotNil(t, page)
	require.Len(t, page, 5)

	// Created topics are named by the index from 0-n, so we can use this
	// index to compare the expected topic name
	for _, topic := range page {
		assert.Equal(t, strconv.Itoa(topicName), topic.Name)
		topicName++
	}

	// 3. Get slice with the last page (11th to 14th - 4 remaining items)
	page, nextPageToken, err = pagination.SliceToPaginatedWithToken(collection, 5, nextPageToken, "name", func(x Topic) string {
		return x.Name
	})
	require.NoError(t, err)
	assert.Empty(t, nextPageToken)
	assert.NotNil(t, page)
	require.Len(t, page, 4)

	// Created topics are named by the index from 0-n, so we can use this
	// index to compare the expected topic name
	for _, topic := range page {
		assert.Equal(t, strconv.Itoa(topicName), topic.Name)
		topicName++
	}
}
