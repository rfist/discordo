package chat

import (
	"context"
	"log/slog"
	"slices"

	"github.com/ayn2op/discordo/internal/http"
	"github.com/ayn2op/discordo/internal/notifications"
	"github.com/ayn2op/tview"
	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/arikawa/v3/session"
	"github.com/diamondburned/arikawa/v3/state"
	"github.com/diamondburned/arikawa/v3/state/store/defaultstore"
	"github.com/diamondburned/arikawa/v3/utils/handler"
	"github.com/diamondburned/arikawa/v3/utils/httputil"
	"github.com/diamondburned/arikawa/v3/utils/httputil/httpdriver"
	"github.com/diamondburned/arikawa/v3/utils/ws"
	"github.com/diamondburned/ningen/v3"
)

func (m *Model) OpenState(token string) error {
	identifyProps := http.IdentifyProperties()
	gateway.DefaultIdentity = identifyProps
	gateway.DefaultPresence = &gateway.UpdatePresenceCommand{
		Status: m.cfg.Status,
	}

	id := gateway.DefaultIdentifier(token)
	id.Compress = false

	session := session.NewCustom(id, http.NewClient(token), handler.New())
	state := state.NewFromSession(session, defaultstore.New())
	m.state = ningen.FromState(state)

	// Handlers
	m.state.AddHandler(m.onRaw)
	m.state.AddHandler(m.onReady)
	m.state.AddHandler(m.onMessageCreate)
	m.state.AddHandler(m.onMessageUpdate)
	m.state.AddHandler(m.onMessageDelete)
	m.state.AddHandler(m.onMessageReactionAdd)
	m.state.AddHandler(m.onMessageReactionRemove)
	m.state.AddHandler(m.onMessageReactionRemoveAll)
	m.state.AddHandler(m.onMessageReactionRemoveEmoji)
	m.state.AddHandler(m.onReadUpdate)
	m.state.AddHandler(m.onGuildMembersChunk)
	m.state.AddHandler(m.onGuildMemberRemove)

	if m.cfg.TypingIndicator.Receive {
		m.state.AddHandler(m.onTypingStart)
	}

	m.state.StateLog = func(err error) {
		slog.Error("state log", "err", err)
	}

	m.state.OnRequest = append(m.state.OnRequest, httputil.WithHeaders(http.Headers()), m.onRequest)
	return m.state.Open(context.Background())
}

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

func (m *Model) onReady(event *gateway.ReadyEvent) {
	m.app.QueueUpdateDraw(func() {
		// Rebuild indexes from scratch so reconnects and account switches do not
		// retain pointers to detached tree nodes.
		m.guildsTree.resetNodeIndex()

		dmNode := tview.NewTreeNode("Direct Messages").SetReference(dmNode{}).SetExpandable(true).SetExpanded(false)
		m.guildsTree.dmRootNode = dmNode

		root := m.guildsTree.
			GetRoot().
			ClearChildren().
			AddChild(dmNode)

		// Track guilds already in folders to find orphans (newly joined guilds may not be synced to GuildFolders yet but always appear in GuildPositions)
		guildsInFolders := make(map[discord.GuildID]bool)
		for _, folder := range event.UserSettings.GuildFolders {
			for _, guildID := range folder.GuildIDs {
				guildsInFolders[guildID] = true
			}
		}

		// Build index of all available guilds.
		guildsByID := make(map[discord.GuildID]*gateway.GuildCreateEvent, len(event.Guilds))
		for index := range event.Guilds {
			guildsByID[event.Guilds[index].ID] = &event.Guilds[index]
		}

		// Use GuildPositions for ordering (it's the canonical order).
		// Guilds not in any folder are "orphans" - add them directly to root.
		positions := event.UserSettings.GuildPositions
		// Fallback: GuildPositions shouldn't be nil but handle gracefully
		if len(positions) == 0 {
			positions = make([]discord.GuildID, 0, len(event.Guilds))
			for _, guildEvent := range event.Guilds {
				positions = append(positions, guildEvent.ID)
			}
		}

		for _, guildID := range positions {
			// Already handled in folder processing below
			if guildsInFolders[guildID] {
				continue
			}

			// Orphan guild - add directly to root in order
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
		m.app.SetFocus(m.guildsTree)
	})
}

func (m *Model) onMessageCreate(message *gateway.MessageCreateEvent) {
	selectedChannel := m.SelectedChannel()
	if selectedChannel != nil && selectedChannel.ID == message.ChannelID {
		m.removeTyper(message.Author.ID)
		m.app.QueueUpdateDraw(func() {
			m.messagesList.addMessage(message.Message)
		})
	}

	if !m.appFocused.Load() || selectedChannel == nil || selectedChannel.ID != message.ChannelID {
		if err := notifications.Notify(m.state, message, m.cfg); err != nil {
			slog.Error("failed to notify", "err", err, "channel_id", message.ChannelID, "message_id", message.ID)
		}
	}
}

func (m *Model) onMessageUpdate(message *gateway.MessageUpdateEvent) {
	if selected := m.SelectedChannel(); selected != nil && selected.ID == message.ChannelID {
		index := slices.IndexFunc(m.messagesList.messages, func(m discord.Message) bool {
			return m.ID == message.ID
		})
		if index < 0 {
			return
		}

		m.app.QueueUpdateDraw(func() {
			m.messagesList.setMessage(index, message.Message)
		})
	}
}

func (m *Model) onMessageDelete(message *gateway.MessageDeleteEvent) {
	if selected := m.SelectedChannel(); selected != nil && selected.ID == message.ChannelID {
		prevCursor := m.messagesList.Cursor()
		deletedIndex := slices.IndexFunc(m.messagesList.messages, func(m discord.Message) bool {
			return m.ID == message.ID
		})
		if deletedIndex < 0 {
			return
		}

		m.app.QueueUpdateDraw(func() {
			m.messagesList.deleteMessage(deletedIndex)
		})

		// Keep cursor stable when possible after removal.
		newCursor := prevCursor
		if prevCursor == deletedIndex {
			// Prefer previous item; fall forward if we deleted the first.
			newCursor = deletedIndex - 1
			if newCursor < 0 {
				if deletedIndex < len(m.messagesList.messages) {
					newCursor = deletedIndex
				} else {
					newCursor = -1
				}
			}
		} else if prevCursor > deletedIndex {
			// Shift back since the list shrank before the cursor.
			newCursor = prevCursor - 1
		}
		if newCursor != prevCursor {
			// Avoid redundant cursor updates if nothing changed.
			m.app.QueueUpdateDraw(func() {
				m.messagesList.SetCursor(newCursor)
			})
		}
	}
}

// updateMessage applies edit to the local copy of a message in the currently
// selected channel and redraws it. The gateway store may evict old messages,
// so the local list is mutated directly instead of re-fetching from the cabinet.
func (m *Model) updateMessage(channelID discord.ChannelID, messageID discord.MessageID, edit func(*discord.Message)) {
	selected := m.SelectedChannel()
	if selected == nil || selected.ID != channelID {
		return
	}

	index := slices.IndexFunc(m.messagesList.messages, func(m discord.Message) bool {
		return m.ID == messageID
	})
	if index < 0 {
		return
	}

	m.app.QueueUpdateDraw(func() {
		message := m.messagesList.messages[index]
		edit(&message)
		m.messagesList.setMessage(index, message)
	})
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
	me, _ := m.state.Cabinet.Me()
	isMe := me != nil && event.UserID == me.ID
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
	me, _ := m.state.Cabinet.Me()
	isMe := me != nil && event.UserID == me.ID
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

func (m *Model) onGuildMembersChunk(event *gateway.GuildMembersChunkEvent) {
	m.messagesList.setFetchingChunk(false, uint(len(event.Members)))
}

func (m *Model) onGuildMemberRemove(event *gateway.GuildMemberRemoveEvent) {
	m.messageInput.cache.Invalidate(event.GuildID.String()+" "+event.User.Username, m.state.MemberState.SearchLimit)
}

func (m *Model) onTypingStart(event *gateway.TypingStartEvent) {
	selectedChannel := m.SelectedChannel()
	if selectedChannel == nil {
		return
	}

	if selectedChannel.ID != event.ChannelID {
		return
	}

	me, _ := m.state.Cabinet.Me()
	if event.UserID == me.ID {
		return
	}

	m.addTyper(event.UserID)
}
