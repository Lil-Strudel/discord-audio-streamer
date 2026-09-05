<script lang="ts">
  import {
    LoadTrack, Pause, PickAudioFile, Play, Resume, SeekTo, Stop,
  } from "../../wailsjs/go/main/App";
  import type { main } from "../../wailsjs/go/models";
  import type { Telemetry } from "../lib/types";
  import { errorText, formatTime } from "../lib/types";
  import LevelMeter from "./LevelMeter.svelte";
  import VolumeSlider from "./VolumeSlider.svelte";

  let {
    status,
    telemetry,
    onError,
  }: {
    status: main.Status;
    telemetry: Telemetry;
    onError: (message: string) => void;
  } = $props();

  let loading = $state(false);
  let scrubbing = $state(false);
  let scrubValue = $state(0);

  let track = $derived(status.track);
  let isPlayer = $derived(status.mode === "player");
  let canPlay = $derived(status.inVoice && !!track);
  let duration = $derived(track?.durationMs ?? 0);

  // The seek bar follows playback except while it is being dragged, so the
  // handle does not fight the position updates arriving ten times a second.
  let position = $derived(scrubbing ? scrubValue : isPlayer ? telemetry.positionMs : 0);

  async function choose() {
    loading = true;
    try {
      const path = await PickAudioFile();
      if (path) await LoadTrack(path);
    } catch (err) {
      onError(errorText(err));
    } finally {
      loading = false;
    }
  }

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
</script>

<div class="player">
  <div class="picker">
    <button onclick={choose} disabled={loading}>
      {loading ? "Reading…" : "Choose an audio file"}
    </button>
    {#if track}
      <div class="track">
        <strong title={track.path}>{track.name}</strong>
        <span>
          {track.codec}{#if duration > 0} · {formatTime(duration)}{/if}
        </span>
      </div>
    {:else}
      <p class="empty">No file loaded.</p>
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
    {#if status.playing}
      <button class="primary" onclick={() => run(Pause)}>Pause</button>
    {:else if status.paused}
      <button class="primary" onclick={() => run(Resume)}>Resume</button>
    {:else}
      <button class="primary" disabled={!canPlay} onclick={() => run(Play)}>Play</button>
    {/if}
    <button disabled={!isPlayer} onclick={() => run(Stop)}>Stop</button>
  </div>

  <div class="output">
    <VolumeSlider value={status.settings.volumePercent} disabled={!status.inVoice} />
    <LevelMeter rms={telemetry.rms} peak={telemetry.peak} active={isPlayer && status.playing} />
  </div>

  {#if !status.inVoice}
    <p class="hint">Join a voice channel above to start playing.</p>
  {/if}
</div>

<style>
  .player {
    display: flex;
    flex-direction: column;
    gap: 20px;
  }

  .picker {
    display: flex;
    align-items: center;
    gap: 16px;
  }

  .picker button {
    flex: 0 0 auto;
  }

  .track {
    display: flex;
    flex-direction: column;
    min-width: 0;
    line-height: 1.35;
  }

  .track strong {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .track span {
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
    gap: 10px;
  }

  .transport button {
    min-width: 92px;
  }

  .output {
    display: flex;
    flex-direction: column;
    gap: 12px;
    padding-top: 4px;
  }

  .hint {
    margin: 0;
    color: var(--warn);
    font-size: 13px;
  }
</style>
