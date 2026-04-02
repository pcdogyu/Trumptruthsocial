package main

import (
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const postsFileName = "posts.json"

type PostStore struct {
	mu    sync.Mutex
	Posts map[string]Post
}

func NewPostStore() (*PostStore, error) {
	store := &PostStore{Posts: map[string]Post{}}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *PostStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := os.ReadFile(postsFileName)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var items []Post
	if err := json.Unmarshal(b, &items); err != nil {
		return err
	}
	for _, post := range items {
		if post.ID == "" {
			continue
		}
		s.Posts[post.ID] = post
	}
	return nil
}

func (s *PostStore) saveLocked() error {
	items := make([]Post, 0, len(s.Posts))
	for _, post := range s.Posts {
		items = append(items, post)
	}
	sortPostsDesc(items)
	b, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(postsFileName, b, 0644)
}

func (s *PostStore) AddPost(post Post) (bool, error) {
	if post.ID == "" {
		return false, errors.New("post id is empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.Posts[post.ID]; exists {
		return false, nil
	}
	post.Timestamp = normalizeTimeString(post.Timestamp)
	s.Posts[post.ID] = post
	return true, s.saveLocked()
}

func (s *PostStore) DeletePost(postID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.Posts[postID]; !exists {
		return false, nil
	}
	delete(s.Posts, postID)
	return true, s.saveLocked()
}

func (s *PostStore) GetPostByID(postID string) (Post, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	post, ok := s.Posts[postID]
	return post, ok
}

func (s *PostStore) MarkSent(postID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	post, ok := s.Posts[postID]
	if !ok {
		return false, nil
	}
	post.SentToTelegram = true
	s.Posts[postID] = post
	return true, s.saveLocked()
}

func (s *PostStore) GetUnsentPosts() []Post {
	s.mu.Lock()
	defer s.mu.Unlock()

	items := make([]Post, 0)
	for _, post := range s.Posts {
		if !post.SentToTelegram {
			items = append(items, post)
		}
	}
	sortPostsAsc(items)
	return items
}

func (s *PostStore) GetAllPosts(username string, limit int) []Post {
	s.mu.Lock()
	defer s.mu.Unlock()

	items := make([]Post, 0)
	for _, post := range s.Posts {
		if username == "" || strings.EqualFold(post.Username, username) {
			items = append(items, post)
		}
	}
	sortPostsDesc(items)
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}

func (s *PostStore) GetUsernames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	set := map[string]struct{}{}
	for _, post := range s.Posts {
		if post.Username != "" {
			set[post.Username] = struct{}{}
		}
	}
	usernames := make([]string, 0, len(set))
	for username := range set {
		usernames = append(usernames, username)
	}
	sort.Strings(usernames)
	return usernames
}

func sortPostsDesc(items []Post) {
	sort.Slice(items, func(i, j int) bool {
		ti := parsePostTime(items[i].Timestamp)
		tj := parsePostTime(items[j].Timestamp)
		if ti.Equal(tj) {
			return items[i].ID > items[j].ID
		}
		return ti.After(tj)
	})
}

func sortPostsAsc(items []Post) {
	sort.Slice(items, func(i, j int) bool {
		ti := parsePostTime(items[i].Timestamp)
		tj := parsePostTime(items[j].Timestamp)
		if ti.Equal(tj) {
			return items[i].ID < items[j].ID
		}
		return ti.Before(tj)
	})
}

func parsePostTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t
	}
	return time.Time{}
}
