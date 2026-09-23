<script lang="ts">
  import {
    ChooseSoundFolder, PauseSound, PlaySound, RenameSoundTrack, ResumeSound,
    SetSoundLoop, SetSoundView, SetSoundVolume, StopSound,
  } from "../../wailsjs/go/main/App";
  import type { main } from "../../wailsjs/go/models";
  import type { ChannelStats } from "../lib/types";
  import { errorText, formatTime } from "../lib/types";
  import LevelMeter from "./LevelMeter.svelte";
  import SoundBrowser from "./SoundBrowser.svelte";
  import VolumeSlider from "./VolumeSlider.svelte";

  let {
    index,
    track,
    stats,
    inVoice,
    onError,
  }: {
    index: number;
    track: main.SoundboardTrack;
    stats: ChannelStats | undefined;
    inVoice: boolean;
    onError: (message: string) => void;
  } = $props();

  let playing = $derived(track.path !== "");
  let nowName = $derived(track.path.split(/[\\/]/).at(-1) ?? "");

  // Same reasoning as the volume slider: show what was asked for until the
  // backend reports it back, so the toggles do not flicker on a round trip.
  let loopRequest = $state<boolean | null>(null);
  let viewRequest = $state<string | null>(null);
  let loop = $derived(loopRequest ?? track.loop);
  let view = $derived(viewRequest ?? track.view);

  $effect(() => {
    if (loopRequest !== null && track.loop === loopRequest) loopRequest = null;
  });
  $effect(() => {
    if (viewRequest !== null && track.view === viewRequest) viewRequest = null;
  });

  // The name is edited in a draft and only saved when the field is left, so
  // every keystroke is not a write to the settings file.
  let draft = $state<string | null>(null);

  async function run(fn: () => Promise<unknown>) {
    try {
      await fn();
    } catch (err) {
      onError(errorText(err));
    }
  }

  function saveName() {
    if (draft === null) return;
    const name = draft;
    draft = null;
    if (name.trim() !== track.name) run(() => RenameSoundTrack(index, name));
  }

  function onNameKey(event: KeyboardEvent) {
    const field = event.currentTarget as HTMLInputElement;
    if (event.key === "Enter") {
      field.blur();
    } else if (event.key === "Escape") {
      // Dropping the draft first makes the blur that follows save nothing.
      draft = null;
      field.blur();
    }
  }

  function toggleLoop() {
    loopRequest = !loop;
    run(() => SetSoundLoop(index, !loop));
  }

  function setView(next: string) {
    viewRequest = next;
    run(() => SetSoundView(index, next));
  }
</script>

<article class="track" class:active={playing && !track.paused}>
  <header>
    <input
      class="name"
      aria-label="Track name"
      maxlength="40"
      spellcheck="false"
      value={draft ?? track.name}
      oninput={(e) => (draft = e.currentTarget.value)}
      onblur={saveName}
      onkeydown={onNameKey}
    />
    <div class="views" role="group" aria-label="View">
      <button class:on={view === "grid"} aria-pressed={view === "grid"} title="Grid" onclick={() => setView("grid")}>
        ▦
      </button>
      <button class:on={view === "list"} aria-pressed={view === "list"} title="List" onclick={() => setView("list")}>
        ☰
      </button>
    </div>
    <button class="folder" title={track.folder || "Choose a folder"} onclick={() => run(() => ChooseSoundFolder(index))}>
      {track.folder ? "Change folder" : "Choose folder"}
    </button>
  </header>

  <div class="now">
    <div class="title">
      {#if playing}
        <strong title={track.path}>{nowName}</strong>
        <span class="time">
          {track.paused ? "Paused · " : ""}{formatTime(stats?.positionMs ?? 0)}
        </span>
      {:else}
        <span class="idle">Nothing playing</span>
      {/if}
    </div>
    <div class="controls">
      <button
        class="mode"
        class:on={loop}
        aria-pressed={loop}
        title={loop ? "Loop on" : "Loop off"}
        onclick={toggleLoop}
      >
        🔁
      </button>
      {#if track.paused}
        <button disabled={!playing} title="Resume" aria-label="Resume" onclick={() => run(() => ResumeSound(index))}>
          ▶
        </button>
      {:else}
        <button disabled={!playing} title="Pause" aria-label="Pause" onclick={() => run(() => PauseSound(index))}>
          ⏸
        </button>
      {/if}
      <button disabled={!playing} title="Stop" aria-label="Stop" onclick={() => run(() => StopSound(index))}>
        ⏹
      </button>
    </div>
  </div>

  <LevelMeter rms={stats?.rms ?? 0} peak={stats?.peak ?? 0} active={playing && !track.paused} />

  <VolumeSlider
    id="track-volume-{index}"
    value={track.volumePercent}
    onchange={(v) => SetSoundVolume(index, v)}
  />

  {#if track.folder}
    <SoundBrowser
      root={track.folder}
      {view}
      playing={track.path}
      disabled={!inVoice}
      onplay={(path) => run(() => PlaySound(index, path))}
    />
  {:else}
    <p class="empty">
      Choose a folder of sounds for this track. Its subfolders show up here too.
    </p>
  {/if}
</article>

<style>
  .track {
    display: flex;
    flex-direction: column;
    gap: 10px;
    min-width: 0;
    padding: 12px 14px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }

  .track.active {
    border-color: color-mix(in srgb, var(--accent) 55%, var(--border));
  }

  header {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .name {
    flex: 1;
    min-width: 0;
    font: inherit;
    font-weight: 700;
    color: var(--text);
    background: none;
    border: 1px solid transparent;
    border-radius: var(--radius-sm);
    padding: 3px 6px;
  }

  .name:hover,
  .name:focus {
    border-color: var(--border);
    background: var(--bg);
    outline: none;
  }

  .views {
    display: flex;
  }

  .views button {
    padding: 4px 9px;
    opacity: 0.5;
  }

  .views button:first-child {
    border-radius: var(--radius-sm) 0 0 var(--radius-sm);
  }

  .views button:last-child {
    border-radius: 0 var(--radius-sm) var(--radius-sm) 0;
    margin-left: -1px;
  }

  .views button.on {
    opacity: 1;
    border-color: var(--accent);
    position: relative;
  }

  .folder {
    padding: 4px 10px;
    font-size: 12.5px;
  }

  .now {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
  }

  .title {
    display: flex;
    flex-direction: column;
    min-width: 0;
    line-height: 1.3;
  }

  .title strong {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .time,
  .idle {
    font-size: 12.5px;
    color: var(--text-faint);
    font-variant-numeric: tabular-nums;
  }

  .controls {
    display: flex;
    gap: 6px;
    flex: none;
  }

  .controls button {
    min-width: 36px;
    padding: 5px 8px;
  }

  .mode {
    opacity: 0.45;
  }

  .mode.on {
    opacity: 1;
    border-color: var(--accent);
  }

  .empty {
    margin: 0;
    padding: 16px 12px;
    text-align: center;
    font-size: 12.5px;
    color: var(--text-faint);
    border: 1px dashed var(--border);
    border-radius: var(--radius-sm);
  }
</style>
