package server

import (
	"testing"
	"time"

	"agenthub/internal/db"
)

func post(id int, parent *int) db.PostWithChannel {
	return db.PostWithChannel{Post: db.Post{ID: id, ParentID: parent, CreatedAt: time.Unix(0, 0)}, ChannelName: "c"}
}

func TestBuildThreadsOrdersByLatestActivity(t *testing.T) {
	one := 1
	// Input arrives newest first, as the DB returns it. All posts share a timestamp.
	posts := []db.PostWithChannel{post(4, &one), post(3, nil), post(2, nil), post(1, nil)}
	th := buildThreads(posts, false, nil, nil)
	if len(th) != 3 {
		t.Fatalf("want 3 threads, got %d", len(th))
	}
	// Thread 1 got a reply (#4) most recently, so it comes first, then 3, then 2.
	got := []int{th[0].Root.ID, th[1].Root.ID, th[2].Root.ID}
	want := []int{1, 3, 2}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("thread order = %v, want %v", got, want)
		}
	}
	if th[0].Root.ReplyCount != 1 || len(th[0].Replies) != 1 {
		t.Fatal("reply was not attached to its thread")
	}
}

func TestBuildThreadsRepliesReadOldestFirst(t *testing.T) {
	root := 1
	posts := []db.PostWithChannel{post(4, &root), post(3, &root), post(2, &root), post(1, nil)}
	th := buildThreads(posts, false, nil, nil)
	r := th[0].Replies
	if len(r) != 3 || r[0].ID != 2 || r[2].ID != 4 {
		t.Fatalf("replies should read oldest to newest, got %v %v %v", r[0].ID, r[1].ID, r[2].ID)
	}
}
