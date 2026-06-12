// Downgrade policy (BACKEND_PLAN Phase B8, SYSTEM_DESIGN §6):
//
// When a user downgrades (subscription expires → spark), existing capsules and
// albums that exceed the new plan's limits are left untouched — members can
// still use them normally. Only NEW creations are blocked: album/capsule create
// handlers count active resources against the user's current plan and return
// 403 when at or over the limit.
package billing
