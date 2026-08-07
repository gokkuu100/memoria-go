# Memoria Push Notifications Guide

This guide outlines how push notifications are implemented and how you can add new triggers (e.g., when a user deletes a capsule or leaves an album).

## Architecture

Memoria uses an **Outbox Pattern** to ensure reliable delivery of push notifications without blocking API requests or losing messages during crashes.

1. **API Handlers (`internal/capsules/http.go`, etc.)**
   - The handler performs its core database logic (e.g., deleting a capsule).
   - Once successful, the handler calls a method on the `notifications.Sender` interface (e.g., `Notify.CapsuleDeleted`).
2. **Notification Service (`internal/notifications/service.go`)**
   - The service builds the notification payload (title, body, data).
   - It writes an entry to the `notification_outbox` table in Postgres using `dbgen.Queries`.
   - The outbox table stores the recipient `user_id`, `push_token`, `title`, `body`, and a status of `pending`.
3. **Background Worker (`internal/notifications/worker.go`)**
   - A dedicated Goroutine polls the `notification_outbox` table.
   - It batches pending messages and sends them to Expo Push API.
   - On success, it marks the rows as `sent` (or deletes them).

## Adding a New Notification Trigger

Let's walk through adding a notification for when a user leaves a capsule.

### 1. Define the Category
Open `internal/notifications/types.go` and add a new constant:
```go
const CategoryCapsuleMemberLeft = "capsule_member_left"
```

### 2. Add Method to Sender Interface
Open `internal/notifications/sender.go` and update the `Sender` interface:
```go
type Sender interface {
    // ... existing methods
    CapsuleMemberLeft(ctx context.Context, recipientID, actorID uuid.UUID, capsuleID uuid.UUID, capsuleName, actorName string)
}
```

### 3. Implement the Method
Open `internal/notifications/service.go` and add the implementation:
```go
func (s *Service) CapsuleMemberLeft(ctx context.Context, recipientID, actorID uuid.UUID, capsuleID uuid.UUID, capsuleName, actorName string) {
    if !s.prefEnabled(ctx, recipientID, CategoryCapsuleMemberLeft) {
        return // User disabled this notification type
    }
    
    title := "Member Left"
    body := fmt.Sprintf("%s has left the capsule '%s'", actorName, capsuleName)
    data := map[string]any{"capsule_id": capsuleID.String()}
    
    // Write to the outbox
    s.enqueue(ctx, recipientID, CategoryCapsuleMemberLeft, title, body, data, "")
}
```

### 4. Trigger from the Handler
In `internal/capsules/handler.go`, inside the `leave` function (after successfully removing the user from the capsule):

```go
// Fetch remaining members to notify
members, err := h.Q.ListCapsuleMemberUserIDs(r.Context(), pg.UUID(capID))
if err == nil && h.Notify != nil {
    actorName := meQuery.DisplayName // or fetch the user's name
    for _, uid := range members {
        h.Notify.CapsuleMemberLeft(r.Context(), pg.UUIDValue(uid), userID, capID, cap.Name, actorName)
    }
}
```

## Summary
By following this pattern, you guarantee that notifications are cleanly decoupled from the HTTP response cycle and are reliably retried if the push service is temporarily down.
