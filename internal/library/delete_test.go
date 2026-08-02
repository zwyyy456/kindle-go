package library

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/flashdict/kindle2flashdict/internal/task"
)

func TestDeleteBookOwnsActiveTaskPolicy(t *testing.T) {
	service, _ := newTestService(t)
	result, err := service.Import(context.Background(), ImportRequest{
		Filename: "book.txt", Reader: strings.NewReader("正文"),
	})
	if err != nil {
		t.Fatal(err)
	}
	tasks := task.NewService(service.store)
	if _, err := tasks.Create(context.Background(), task.CreateRequest{
		BookID: result.Book.ID, Type: task.GenerateEPUB, InputFileID: result.Book.Original.ID,
	}); err != nil {
		t.Fatal(err)
	}

	err = service.DeleteBook(context.Background(), result.Book.ID)
	var deletionErr *DeletionError
	if !errors.As(err, &deletionErr) || deletionErr.Code != "book_has_active_tasks" || !strings.Contains(deletionErr.Error(), "cancel them") {
		t.Fatalf("DeleteBook error = %#v, %v", deletionErr, err)
	}
}

func TestDeleteFileOwnsProtectedOriginalPolicy(t *testing.T) {
	service, _ := newTestService(t)
	result, err := service.Import(context.Background(), ImportRequest{
		Filename: "book.txt", Reader: strings.NewReader("正文"),
	})
	if err != nil {
		t.Fatal(err)
	}

	err = service.DeleteFile(context.Background(), result.Book.Original.ID)
	var deletionErr *DeletionError
	if !errors.As(err, &deletionErr) || deletionErr.Code != "protected_file" {
		t.Fatalf("DeleteFile error = %#v, %v", deletionErr, err)
	}
}
