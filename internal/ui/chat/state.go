package chat

import (
	"log/slog"
	"slices"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/utils/httputil/httpdriver"
	"github.com/ayn2op/arikawa/v3/utils/ws"
	"github.com/ayn2op/discordo/internal/notifications"
	"github.com/ayn2op/ningen/v3"
	"github.com/ayn2op/ningen/v3/states/read"
	"github.com/ayn2op/tview"
	"github.com/ayn2op/tview/tree"
)

func (m *Model) onRequest(r httpdriver.Request) error {
	if req, ok := r.(*httpdriver.DefaultRequest); ok {
		slog.Debug("new HTTP request", "method", req.Method, "url", req.URL)
	}
	return nil
}

func (m *Model) onRaw(event *ws.RawEvent) {
	slog.Debug(
		"new raw event",
		"code", event.OriginalCode,
		"type", event.OriginalType,
		// "data", event.Raw,
	)
}

func (m *Model) onReady(event *gateway.ReadyEvent) tview.Cmd {
	// Rebuild indexes from scratch so reconnects and account switches do not
	// retain pointers to detached tree nodes.
	m.guildsTree.resetNodeIndex()

	dmNode := tree.NewNode("Direct Messages").SetReference(dmNode{}).SetExpandable(true).SetExpanded(false)
	m.guildsTree.dmRootNode = dmNode

	root := m.guildsTree.
		Root().
		ClearChildren().
		AddChild(dmNode)

	// Track guilds already in folders to find orphans.
	// Newly joined guilds may not be synced to GuildFolders yet but always appear in guild positions.
	guildsInFolders := make(map[discord.GuildID]bool)
	for _, folder := range event.UserSettings.GuildFolders {
		for _, guildID := range folder.GuildIDs {
			guildsInFolders[guildID] = true
		}
	}

	guildsByID := make(map[discord.GuildID]*gateway.GuildCreateEvent, len(event.Guilds))
	for index := range event.Guilds {
		guildsByID[event.Guilds[index].ID] = &event.Guilds[index]
	}

	// Use GuildPositions for ordering (it's the canonical order).
	// Guilds not in any folder are "orphans" - add them directly to root.
	positions := event.UserSettings.GuildPositions
	// GuildPositions is normally set; fall back to event order if not.
	if len(positions) == 0 {
		positions = make([]discord.GuildID, 0, len(event.Guilds))
		for _, guildEvent := range event.Guilds {
			positions = append(positions, guildEvent.ID)
		}
	}

	for _, guildID := range positions {
		// Already handled in folder processing below.
		if guildsInFolders[guildID] {
			continue
		}

		if guildEvent, ok := guildsByID[guildID]; ok {
			m.guildsTree.createGuildNode(root, guildEvent.Guild)
		}
	}

	// Process folders (real folders and single-guild "folders")
	for _, folder := range event.UserSettings.GuildFolders {
		if folder.ID == 0 && len(folder.GuildIDs) == 1 {
			if guild, ok := guildsByID[folder.GuildIDs[0]]; ok {
				m.guildsTree.createGuildNode(root, guild.Guild)
			}
		} else {
			m.guildsTree.createFolderNode(folder, guildsByID)
		}
	}

	m.guildsTree.SetCurrentNode(root)
	return tview.SetFocus(m.guildsTree)
}

func (m *Model) onMessageCreate(message *gateway.MessageCreateEvent) tview.Cmd {
	selectedChannel, ok := m.SelectedChannel()
	if ok && selectedChannel.ID == message.ChannelID {
		m.removeTyper(message.Author.ID)
		m.messagesList.addMessage(message.Message)
		// Notify for the selected channel too when the terminal is unfocused.
		if m.appFocused.Load() {
			return nil
		}
	}

	return m.notify(*message)
}

func (m *Model) notify(message gateway.MessageCreateEvent) tview.Cmd {
	return func() tview.Msg {
		if err := notifications.Notify(m.state, message, m.cfg); err != nil {
			slog.Error("failed to notify", "err", err, "channel_id", message.ChannelID, "message_id", message.ID)
		}
		return nil
	}
}

func (m *Model) onMessageUpdate(message *gateway.MessageUpdateEvent) {
	selectedChannel, ok := m.SelectedChannel()
	if !ok || selectedChannel.ID != message.ChannelID {
		return
	}

	index := slices.IndexFunc(m.messagesList.messages, func(m discord.Message) bool {
		return m.ID == message.ID
	})
	if index < 0 {
		return
	}

	m.messagesList.setMessage(index, message.Message)
}

func (m *Model) onMessageDelete(message *gateway.MessageDeleteEvent) {
	selectedChannel, ok := m.SelectedChannel()
	if !ok || selectedChannel.ID != message.ChannelID {
		return
	}

	prevCursor := m.messagesList.Cursor()
	deletedIndex := slices.IndexFunc(m.messagesList.messages, func(m discord.Message) bool {
		return m.ID == message.ID
	})
	if deletedIndex < 0 {
		return
	}

	m.messagesList.deleteMessage(deletedIndex)

	newCursor := cursorAfterDelete(prevCursor, deletedIndex, len(m.messagesList.messages))
	if newCursor != prevCursor {
		m.messagesList.SetCursor(newCursor)
	}
}

// updateMessage applies edit to the local copy of a message in the currently
// selected channel and re-renders it. The gateway store may evict old
// messages, so the local list is mutated directly instead of re-fetching from
// the cabinet.
func (m *Model) updateMessage(channelID discord.ChannelID, messageID discord.MessageID, edit func(*discord.Message)) {
	selectedChannel, ok := m.SelectedChannel()
	if !ok || selectedChannel.ID != channelID {
		return
	}

	index := slices.IndexFunc(m.messagesList.messages, func(m discord.Message) bool {
		return m.ID == messageID
	})
	if index < 0 {
		return
	}

	message := m.messagesList.messages[index]
	edit(&message)
	m.messagesList.setMessage(index, message)
}

func findReaction(reactions []discord.Reaction, emoji discord.Emoji) int {
	return slices.IndexFunc(reactions, func(r discord.Reaction) bool {
		if emoji.ID.IsValid() {
			return r.Emoji.ID == emoji.ID
		}
		return r.Emoji.Name == emoji.Name
	})
}

func (m *Model) onMessageReactionAdd(event *gateway.MessageReactionAddEvent) {
	isMe := m.isMe(event.UserID)
	m.updateMessage(event.ChannelID, event.MessageID, func(message *discord.Message) {
		// Clone before mutating; the backing array may be shared with the
		// gateway store or previously rendered rows.
		message.Reactions = slices.Clone(message.Reactions)
		if i := findReaction(message.Reactions, event.Emoji); i >= 0 {
			message.Reactions[i].Count++
			message.Reactions[i].Me = message.Reactions[i].Me || isMe
		} else {
			message.Reactions = append(message.Reactions, discord.Reaction{
				Count: 1,
				Me:    isMe,
				Emoji: event.Emoji,
			})
		}
	})
}

func (m *Model) onMessageReactionRemove(event *gateway.MessageReactionRemoveEvent) {
	isMe := m.isMe(event.UserID)
	m.updateMessage(event.ChannelID, event.MessageID, func(message *discord.Message) {
		i := findReaction(message.Reactions, event.Emoji)
		if i < 0 {
			return
		}

		message.Reactions = slices.Clone(message.Reactions)
		message.Reactions[i].Count--
		if message.Reactions[i].Count <= 0 {
			message.Reactions = slices.Delete(message.Reactions, i, i+1)
		} else if isMe {
			message.Reactions[i].Me = false
		}
	})
}

func (m *Model) onMessageReactionRemoveAll(event *gateway.MessageReactionRemoveAllEvent) {
	m.updateMessage(event.ChannelID, event.MessageID, func(message *discord.Message) {
		message.Reactions = nil
	})
}

func (m *Model) onMessageReactionRemoveEmoji(event *gateway.MessageReactionRemoveEmojiEvent) {
	m.updateMessage(event.ChannelID, event.MessageID, func(message *discord.Message) {
		i := findReaction(message.Reactions, event.Emoji)
		if i < 0 {
			return
		}

		message.Reactions = slices.Delete(slices.Clone(message.Reactions), i, i+1)
	})
}

func cursorAfterDelete(prevCursor, deletedIndex, remaining int) int {
	switch {
	case prevCursor > deletedIndex:
		return prevCursor - 1
	case prevCursor == deletedIndex:
		if prev := deletedIndex - 1; prev >= 0 {
			return prev
		}
		return min(deletedIndex, remaining-1)
	default:
		return prevCursor
	}
}

func (m *Model) onGuildMembersChunk(event *gateway.GuildMembersChunkEvent) tview.Cmd {
	m.messagesList.invalidateRenderedMessages()
	return m.composer.onGuildMembersChunk(event)
}

func (m *Model) onGuildMemberRemove(event *gateway.GuildMemberRemoveEvent) {
	m.composer.cache.Invalidate(event.GuildID.String()+" "+event.User.Username, m.state.MemberState.SearchLimit)
}

func (m *Model) onTypingStart(event *gateway.TypingStartEvent) {
	selectedChannel, ok := m.SelectedChannel()
	if !ok || selectedChannel.ID != event.ChannelID {
		return
	}

	if m.isMe(event.UserID) {
		return
	}

	m.addTyper(event.UserID)
}

func (m *Model) onReadUpdate(event *read.UpdateEvent) {
	// Use indexed node lookup to avoid walking the whole tree on every read event.
	// This runs frequently while reading/typing across channels.
	if event.GuildID.IsValid() {
		if guildNode := m.guildsTree.findNodeByReference(event.GuildID); guildNode != nil {
			m.guildsTree.setNodeLineStyle(guildNode, m.guildsTree.guildNodeStyle(event.GuildID))
		}
	}

	// Channel style is always updated for the target channel regardless of
	// whether it's in a guild or DM.
	if channelNode := m.guildsTree.findNodeByReference(event.ChannelID); channelNode != nil {
		channel, err := m.state.Cabinet.Channel(event.ChannelID)
		if err != nil {
			indication := m.state.ChannelIsUnread(event.ChannelID, ningen.UnreadOpts{IncludeMutedCategories: true})
			m.guildsTree.setNodeLineStyle(channelNode, m.guildsTree.unreadStyle(indication))
			return
		}
		m.guildsTree.setNodeLineStyle(channelNode, m.guildsTree.channelNodeStyle(*channel))
	}
}
