<script lang="ts">
  import { OnFileDrop, OnFileDropOff } from "../../wailsjs/runtime/runtime";
  import {
    AddFiles, NextTrack, Pause, Play, PreviousTrack, Resume, SeekTo,
    SetRepeat, SetShuffle, Stop,
  } from "../../wailsjs/go/main/App";
  import type { main, playlist } from "../../wailsjs/go/models";
  import type { Telemetry } from "../lib/types";
  import { errorText, formatTime } from "../lib/types";
  import LevelMeter from "./LevelMeter.svelte";
  import QueueList from "./QueueList.svelte";
  import StreamHealth from "./StreamHealth.svelte";
  import VolumeSlider from "./VolumeSlider.svelte";

  let {
    status,
    telemetry,
    queue,
    onError,
  }: {
    status: main.Status;
    telemetry: Telemetry;
    queue: playlist.State;
    onError: (message: string) => void;
  } = $props();

  let scrubbing = $state(false);
  let scrubValue = $state(0);

  let track = $derived(status.track);
  let isPlayer = $derived(status.mode === "player");
  let canPlay = $derived(status.inVoice && queue.tracks.length > 0);
  let duration = $derived(track?.durationMs ?? 0);

  // The seek bar follows playback except while it is being dragged, so the
  // handle does not fight the position updates arriving ten times a second.
  let position = $derived(scrubbing ? scrubValue : isPlayer ? telemetry.positionMs : 0);

  // Same reasoning as the volume slider: show what was asked for until the
  // backend reports it back, so the toggle does not flicker on a round trip.
  let shuffleRequest = $state<boolean | null>(null);
  let repeatRequest = $state<string | null>(null);
  let shuffle = $derived(shuffleRequest ?? queue.shuffle);
  let repeat = $derived(repeatRequest ?? queue.repeat);

  $effect(() => {
    if (shuffleRequest !== null && queue.shuffle === shuffleRequest) shuffleRequest = null;
  });
  $effect(() => {
    if (repeatRequest !== null && queue.repeat === repeatRequest) repeatRequest = null;
  });

  // Files dropped on the window join the queue. The listener is registered here
  // rather than globally so that dropping onto the desktop-audio tab, where a
  // playlist means nothing, does nothing.
  $effect(() => {
    OnFileDrop((_x, _y, paths) => {
      if (paths?.length) run(() => AddFiles(paths));
    }, false);
    return () => OnFileDropOff();
  });

  async function run(fn: () => Promise<unknown>) {
    try {
      await fn();
    } catch (err) {
      onError(errorText(err));
    }
  }

  function commitSeek(ms: number) {
    scrubbing = false;
    if (canPlay) run(() => SeekTo(Math.round(ms)));
  }

  const repeatLabels: Record<string, string> = {
    off: "Repeat off",
    all: "Repeat all",
    one: "Repeat one",
  };

  function cycleRepeat() {
    const next = repeat === "off" ? "all" : repeat === "all" ? "one" : "off";
    repeatRequest = next;
    run(() => SetRepeat(next));
  }

  function toggleShuffle() {
    shuffleRequest = !shuffle;
    run(() => SetShuffle(!shuffle));
  }
</script>

<div class="player">
  <QueueList {queue} {canPlay} linkError={status.linkError} {onError} />

  <div class="now">
    {#if track}
      <strong title={track.path}>{track.name}</strong>
      <span>
        {track.codec}{#if duration > 0} · {formatTime(duration)}{/if}
      </span>
    {:else}
      <p class="empty">Nothing playing.</p>
    {/if}
  </div>

  <div class="seek">
    <span class="time">{formatTime(position)}</span>
    <input
      type="range"
      min="0"
      max={Math.max(duration, 1)}
      step="100"
      value={position}
      disabled={!canPlay || duration === 0}
      oninput={(e) => { scrubbing = true; scrubValue = Number(e.currentTarget.value); }}
      onchange={(e) => commitSeek(Number(e.currentTarget.value))}
    />
    <span class="time">{formatTime(duration)}</span>
  </div>

  <div class="transport">
    <button
      class="skip"
      disabled={!canPlay}
      title="Previous"
      aria-label="Previous track"
      onclick={() => run(PreviousTrack)}
    >
      ⏮
    </button>

    {#if status.playing}
      <button class="primary" onclick={() => run(Pause)}>Pause</button>
    {:else if status.paused}
      <button class="primary" onclick={() => run(Resume)}>Resume</button>
    {:else}
      <button class="primary" disabled={!canPlay} onclick={() => run(Play)}>Play</button>
    {/if}

    <button
      class="skip"
      disabled={!canPlay}
      title="Next"
      aria-label="Next track"
      onclick={() => run(NextTrack)}
    >
      ⏭
    </button>

    <button disabled={!isPlayer} onclick={() => run(Stop)}>Stop</button>

    <div class="modes">
      <button
        class="mode"
        class:on={shuffle}
        title={shuffle ? "Shuffle on" : "Shuffle off"}
        aria-pressed={shuffle}
        onclick={toggleShuffle}
      >
        🔀
      </button>
      <button
        class="mode"
        class:on={repeat !== "off"}
        title={repeatLabels[repeat] ?? "Repeat off"}
        onclick={cycleRepeat}
      >
        {repeat === "one" ? "🔂" : "🔁"}
      </button>
    </div>
  </div>

  <div class="output">
    <VolumeSlider value={status.settings.volumePercent} disabled={!status.inVoice} />
    <LevelMeter rms={telemetry.rms} peak={telemetry.peak} active={isPlayer && status.playing} />
  </div>

  <details>
    <summary>Stream health</summary>
    <StreamHealth {telemetry} live={false} />
  </details>

  {#if !status.inVoice}
    <p class="hint">Join a voice channel above to start playing.</p>
  {/if}
</div>

<style>
  .player {
    display: flex;
    flex-direction: column;
    gap: 18px;
  }

  .now {
    display: flex;
    flex-direction: column;
    min-width: 0;
    line-height: 1.35;
  }

  .now strong {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .now span {
    font-size: 12.5px;
    color: var(--text-faint);
  }

  .empty {
    margin: 0;
    color: var(--text-faint);
  }

  .seek {
    display: grid;
    grid-template-columns: 52px 1fr 52px;
    align-items: center;
    gap: 12px;
  }

  .time {
    font-variant-numeric: tabular-nums;
    font-size: 12.5px;
    color: var(--text-dim);
    text-align: center;
  }

  .transport {
    display: flex;
    align-items: center;
    gap: 10px;
  }

  .transport button {
    min-width: 92px;
  }

  .transport .skip,
  .transport .mode {
    min-width: 40px;
    padding-inline: 10px;
  }

  /* The playback modes are settings rather than actions, so they sit apart
     from the transport buttons. */
  .modes {
    display: flex;
    gap: 6px;
    margin-left: auto;
  }

  .mode {
    opacity: 0.45;
  }

  .mode.on {
    opacity: 1;
    border-color: var(--accent);
  }

  .output {
    display: flex;
    flex-direction: column;
    gap: 12px;
    padding-top: 4px;
  }

  details {
    border-top: 1px solid var(--border);
    padding-top: 14px;
  }

  summary {
    cursor: pointer;
    color: var(--text-dim);
    font-size: 13px;
  }

  .hint {
    margin: 0;
    color: var(--warn);
    font-size: 13px;
  }
</style>
