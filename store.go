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
		if post.Status == "" {
			post.Status = PostStatusNormal
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
	if post.Status == "" {
		post.Status = PostStatusNormal
	}
	s.Posts[post.ID] = post
	return true, s.saveLocked()
}

func (s *PostStore) UpsertPost(post Post) (bool, error) {
	if post.ID == "" {
		return false, errors.New("post id is empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.Posts[post.ID]
	post.Timestamp = normalizeTimeString(post.Timestamp)
	if exists {
		post = mergePost(existing, post)
		if post == existing {
			return false, nil
		}
	}
	if post.Status == "" {
		post.Status = PostStatusNormal
	}
	s.Posts[post.ID] = post
	return !exists, s.saveLocked()
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

func (s *PostStore) UpdatePostTranslation(postID, translatedContent, translatedAt, translationError string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	post, ok := s.Posts[postID]
	if !ok {
		return false, nil
	}

	post.TranslatedContent = strings.TrimSpace(translatedContent)
	post.TranslatedAt = strings.TrimSpace(translatedAt)
	post.TranslationError = strings.TrimSpace(translationError)
	if post == s.Posts[postID] {
		return true, nil
	}
	s.Posts[postID] = post
	return true, s.saveLocked()
}

func (s *PostStore) GetUnsentPosts() []Post {
	s.mu.Lock()
	defer s.mu.Unlock()

	items := make([]Post, 0)
	for _, post := range s.Posts {
		if post.Status != PostStatusBlocked && !post.SentToTelegram {
			items = append(items, post)
		}
	}
	sortPostsAsc(items)
	return items
}

func (s *PostStore) GetAllPosts(username string, limit int, offset int) []Post {
	s.mu.Lock()
	defer s.mu.Unlock()

	items := make([]Post, 0)
	for _, post := range s.Posts {
		if username == "" || strings.EqualFold(post.Username, username) {
			if post.Status == "" {
				post.Status = PostStatusNormal
			}
			items = append(items, post)
		}
	}
	sortPostsDesc(items)
	if offset < 0 {
		offset = 0
	}
	if offset >= len(items) {
		return []Post{}
	}
	if offset > 0 {
		items = items[offset:]
	}
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

func (s *PostStore) SetStatus(postID, status string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	post, ok := s.Posts[postID]
	if !ok {
		return false, nil
	}
	post.Status = normalizeStatus(status)
	if post.Status == PostStatusBlocked {
		post.SentToTelegram = true
	}
	s.Posts[postID] = post
	return true, s.saveLocked()
}

func (s *PostStore) BulkSetStatus(postIDs []string, status string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	updated := 0
	normalized := normalizeStatus(status)
	for _, postID := range postIDs {
		post, ok := s.Posts[postID]
		if !ok {
			continue
		}
		post.Status = normalized
		if normalized == PostStatusBlocked {
			post.SentToTelegram = true
		}
		s.Posts[postID] = post
		updated++
	}
	if updated > 0 {
		return updated, s.saveLocked()
	}
	return 0, nil
}

func (s *PostStore) BulkDelete(postIDs []string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	deleted := 0
	for _, postID := range postIDs {
		if _, ok := s.Posts[postID]; !ok {
			continue
		}
		delete(s.Posts, postID)
		deleted++
	}
	if deleted > 0 {
		return deleted, s.saveLocked()
	}
	return 0, nil
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

func normalizeStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case PostStatusBlocked:
		return PostStatusBlocked
	default:
		return PostStatusNormal
	}
}

func mergePost(existing, incoming Post) Post {
	merged := existing
	if incoming.Username != "" {
		merged.Username = incoming.Username
	}
	if incoming.Content != "" {
		merged.Content = incoming.Content
	}
	if incoming.TranslatedContent != "" {
		merged.TranslatedContent = incoming.TranslatedContent
	}
	if incoming.TranslatedAt != "" {
		merged.TranslatedAt = incoming.TranslatedAt
	}
	if incoming.TranslationError != "" {
		merged.TranslationError = incoming.TranslationError
	} else if incoming.TranslatedContent != "" {
		merged.TranslationError = ""
	}
	if incoming.WebURL != "" {
		merged.WebURL = incoming.WebURL
	}
	if incoming.ImageURL != "" {
		merged.ImageURL = incoming.ImageURL
	}
	if incoming.VideoURL != "" {
		merged.VideoURL = incoming.VideoURL
	}
	if incoming.Timestamp != "" {
		merged.Timestamp = incoming.Timestamp
	}
	if incoming.RawData != "" {
		merged.RawData = incoming.RawData
	}
	if incoming.Status != "" {
		merged.Status = normalizeStatus(incoming.Status)
	}
	if existing.Status == PostStatusBlocked || incoming.Status == PostStatusBlocked {
		merged.Status = PostStatusBlocked
	}
	if incoming.SentToTelegram || existing.SentToTelegram {
		merged.SentToTelegram = true
	}
	return merged
}
