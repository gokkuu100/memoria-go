package billing

// LimitsDTO is the plan limits payload for GET /v1/me/subscription.
// Null fields mean unlimited.
type LimitsDTO struct {
	MaxActiveCapsules     *int   `json:"max_active_capsules"`
	MaxCapsuleMembers     *int   `json:"max_capsule_members"`
	MaxUnlockDistanceDays int    `json:"max_unlock_distance_days"`
	MaxCapsuleMemories    *int   `json:"max_capsule_memories"`
	ViewabilityDays       *int   `json:"viewability_days"`
	MaxActiveAlbums       *int   `json:"max_active_albums"`
	MaxAlbumMembers       int    `json:"max_album_members"`
	MaxAlbumPhotos        *int   `json:"max_album_photos"`
	AlbumLifespanDays     *int   `json:"album_lifespan_days"`
	AlbumArchiveDays      *int   `json:"album_archive_days"`
	MaxVideoSeconds       int    `json:"max_video_seconds"`
	VideoSound            bool   `json:"video_sound"`
	MaxVoiceSeconds       int    `json:"max_voice_seconds"`
	MaxPhotoBytes         int64  `json:"max_photo_bytes"`
	MaxVideoBytes         int64  `json:"max_video_bytes"`
	MaxVoiceBytes         int64  `json:"max_voice_bytes"`
}

func optionalInt(v int) *int {
	if IsUnlimited(v) {
		return nil
	}
	return &v
}

// LimitsForPlan converts the internal plan table row to an API-friendly DTO.
func LimitsForPlan(p Plan) LimitsDTO {
	return LimitsDTO{
		MaxActiveCapsules:     optionalInt(p.MaxActiveCapsules),
		MaxCapsuleMembers:     optionalInt(p.MaxCapsuleMembers),
		MaxUnlockDistanceDays: p.MaxUnlockDistanceDays,
		MaxCapsuleMemories:    optionalInt(p.MaxCapsuleMemories),
		ViewabilityDays:       optionalInt(p.ViewabilityDays),
		MaxActiveAlbums:       optionalInt(p.MaxActiveAlbums),
		MaxAlbumMembers:       p.MaxAlbumMembers,
		MaxAlbumPhotos:        optionalInt(p.MaxAlbumPhotos),
		AlbumLifespanDays:     optionalInt(p.AlbumLifespanDays),
		AlbumArchiveDays:      optionalInt(p.AlbumArchiveDays),
		MaxVideoSeconds:       p.MaxVideoSeconds,
		VideoSound:            p.VideoSound,
		MaxVoiceSeconds:       p.MaxVoiceSeconds,
		MaxPhotoBytes:         p.MaxPhotoBytes,
		MaxVideoBytes:         p.MaxVideoBytes,
		MaxVoiceBytes:         p.MaxVoiceBytes,
	}
}

func planTier(plan string) int {
	switch plan {
	case PlanPlus:
		return 1
	case PlanPro:
		return 2
	default:
		return 0
	}
}

func IsUpgrade(from, to string) bool {
	return planTier(to) > planTier(from)
}
