<script lang="ts">
  import {
    AddFiles, AddFolder, AddLink, ClearQueue, MoveTrack, PickAudioFiles,
    PickFolder, PlayTrack, RemoveTrack,
  } from "../../wailsjs/go/main/App";
  import type { playlist } from "../../wailsjs/go/models";
  import { errorText, formatTime } from "../lib/types";

  let {
    queue,
    canPlay,
    linkError,
    onError,
  }: {
    queue: playlist.State;
    canPlay: boolean;
    /** Non-empty when yt-dlp is missing, so links cannot be added at all. */
    linkError: string;
    onError: (message: string) => void;
  } = $props();

  let adding = $state(false);

  // The link field is revealed rather than always present, so the queue keeps
  // the vertical space when nobody is pasting anything.
  let linking = $state(false);
  let link = $state("");
  let field = $state<HTMLInputElement | null>(null);
  // dragging is the id being carried; over is the row it is hovering, so the
  // drop line can be drawn before the pointer is released.
  let dragging = $state<string | null>(null);
  let over = $state<number | null>(null);

  async function run(fn: () => Promise<unknown>) {
    try {
      await fn();
    } catch (err) {
      onError(errorText(err));
    }
  }

  async function addFiles() {
    adding = true;
    try {
      const paths = await PickAudioFiles();
      if (paths?.length) await AddFiles(paths);
    } catch (err) {
      onError(errorText(err));
    } finally {
      adding = false;
    }
  }

  async function addFolder() {
    adding = true;
    try {
      const dir = await PickFolder();
      if (dir) await AddFolder(dir);
    } catch (err) {
      onError(errorText(err));
    } finally {
      adding = false;
    }
  }

  function toggleLinking() {
    linking = !linking;
    if (!linking) link = "";
  }

  // Focus follows the field appearing, so the button click lands the cursor
  // where the next keystroke is meant to go.
  $effect(() => {
    if (linking) field?.focus();
  });

  async function addLink() {
    const value = link.trim();
    if (!value || adding) return;

    adding = true;
    try {
      await AddLink(value);
      // Cleared but left open: a playlist is usually pasted a few links at a
      // time, and reopening the field for each would be tedious.
      link = "";
      field?.focus();
    } catch (err) {
      onError(errorText(err));
    } finally {
      adding = false;
    }
  }

  function onLinkKey(event: KeyboardEvent) {
    if (event.key === "Enter") {
      event.preventDefault();
      addLink();
    } else if (event.key === "Escape") {
      event.preventDefault();
      toggleLinking();
    }
  }

  function onDrop(to: number) {
    const id = dragging;
    dragging = null;
    over = null;
    if (id) run(() => MoveTrack(id, to));
  }
</script>

<div class="queue">
  <div class="actions">
    <button onclick={addFiles} disabled={adding}>Add files</button>
    <button onclick={addFolder} disabled={adding}>Add folder</button>
    <button
      class:on={linking}
      onclick={toggleLinking}
      disabled={adding || !!linkError}
      title={linkError || "Queue a YouTube video or playlist"}
    >
      Add link
    </button>
    <span class="count">
      {#if queue.tracks.length}
        {queue.tracks.length} track{queue.tracks.length === 1 ? "" : "s"}
      {/if}
    </span>
    <button
      class="danger"
      disabled={queue.tracks.length === 0}
      onclick={() => run(ClearQueue)}
    >
      Clear
    </button>
  </div>

  {#if linking}
    <div class="link">
      <input
        bind:this={field}
        bind:value={link}
        type="url"
        placeholder="Paste a YouTube link"
        spellcheck="false"
        autocomplete="off"
        disabled={adding}
        onkeydown={onLinkKey}
      />
      <button onclick={addLink} disabled={adding || link.trim() === ""}>
        {adding ? "Adding…" : "Add"}
      </button>
    </div>
  {/if}

  {#if queue.tracks.length === 0}
    <p class="empty">
      Nothing queued. Add files or a folder, paste a YouTube link, or drag
      files onto the window.
    </p>
  {:else}
    <ol>
      {#each queue.tracks as track, i (track.id)}
        <li
          class:current={track.id === queue.currentId}
          class:over={over === i}
          draggable="true"
          ondragstart={() => (dragging = track.id)}
          ondragend={() => { dragging = null; over = null; }}
          ondragover={(e) => { e.preventDefault(); over = i; }}
          ondragleave={() => { if (over === i) over = null; }}
          ondrop={(e) => { e.preventDefault(); onDrop(i); }}
        >
          <span class="index">{i + 1}</span>
          <button
            class="name"
            title={track.path}
            disabled={!canPlay}
            onclick={() => run(() => PlayTrack(track.id))}
          >
            {track.name}
          </button>
          {#if track.source === "youtube"}
            <span class="badge" title="From YouTube">YT</span>
          {:else}
            <span></span>
          {/if}
          <span class="meta">
            {#if track.durationMs > 0}{formatTime(track.durationMs)}{:else}—{/if}
          </span>
          <button
            class="remove"
            aria-label="Remove {track.name}"
            title="Remove"
            onclick={() => run(() => RemoveTrack(track.id))}
          >
            ×
          </button>
        </li>
      {/each}
    </ol>
  {/if}
</div>

<style>
  .queue {
    display: flex;
    flex-direction: column;
    gap: 10px;
    min-height: 0;
  }

  .actions {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  /* The link field sits under the buttons rather than beside them: a URL is
     long, and squeezing it into the action row would leave it unreadable. */
  .link {
    display: flex;
    gap: 8px;
  }

  .link input {
    flex: 1;
    min-width: 0;
  }

  .actions button.on {
    border-color: var(--accent);
  }

  .badge {
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.04em;
    color: var(--text-faint);
    border: 1px solid var(--border);
    border-radius: 3px;
    padding: 1px 4px;
  }

  li.current .badge {
    color: var(--accent);
    border-color: var(--accent);
  }

  .count {
    flex: 1;
    font-size: 12.5px;
    color: var(--text-faint);
  }

  .empty {
    margin: 0;
    padding: 20px 14px;
    text-align: center;
    color: var(--text-faint);
    background: var(--surface);
    border: 1px dashed var(--border);
    border-radius: var(--radius-sm);
  }

  ol {
    /* The list is the only part of the screen that grows without bound, so it
       scrolls rather than pushing the transport controls off the bottom. */
    max-height: 320px;
    overflow-y: auto;
    margin: 0;
    padding: 0;
    list-style: none;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
  }

  li {
    display: grid;
    grid-template-columns: 28px 1fr auto auto 28px;
    align-items: center;
    gap: 8px;
    padding: 5px 8px 5px 4px;
    border-bottom: 1px solid var(--border);
    cursor: grab;
  }

  li:last-child {
    border-bottom: none;
  }

  li:hover {
    background: var(--surface-raised);
  }

  /* Where the dragged row will land. */
  li.over {
    box-shadow: inset 0 2px 0 var(--accent);
  }

  li.current {
    background: color-mix(in srgb, var(--accent) 16%, var(--surface));
  }

  .index {
    text-align: right;
    font-variant-numeric: tabular-nums;
    font-size: 12px;
    color: var(--text-faint);
  }

  li.current .index {
    color: var(--accent);
  }

  .name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    text-align: left;
    background: none;
    border: none;
    border-radius: 0;
    padding: 2px 0;
  }

  .name:hover:not(:disabled) {
    background: none;
    text-decoration: underline;
  }

  .meta {
    font-variant-numeric: tabular-nums;
    font-size: 12.5px;
    color: var(--text-faint);
  }

  .remove {
    background: none;
    border: none;
    padding: 0;
    font-size: 16px;
    line-height: 1;
    color: var(--text-faint);
  }

  .remove:hover {
    background: none;
    color: var(--bad);
  }
</style>
