<script lang="ts">
  import {
    ListCaptureDevices, SetCaptureBufferFrames, StartCapture, StopCapture,
  } from "../../wailsjs/go/main/App";
  import type { ffmpeg, main } from "../../wailsjs/go/models";
  import type { Telemetry } from "../lib/types";
  import { errorText } from "../lib/types";
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

  let devices = $state<ffmpeg.CaptureDevice[]>([]);
  let selected = $state("");
  let refreshing = $state(false);
  let listed = $state(false);

  let streaming = $derived(status.mode === "capture");
  // A monitor or virtual-cable device is the only kind that carries desktop
  // audio, so its absence is the single most likely reason this screen fails.
  let hasLoopback = $derived(devices.some((d) => d.isLoopback));

  async function refresh() {
    refreshing = true;
    try {
      devices = (await ListCaptureDevices()) ?? [];
      listed = true;
      if (!devices.some((d) => d.id === selected)) {
        const remembered = devices.find((d) => d.id === status.settings.lastCaptureDeviceId);
        selected = (remembered ?? devices.find((d) => d.isLoopback) ?? devices[0])?.id ?? "";
      }
    } catch (err) {
      onError(errorText(err));
    } finally {
      refreshing = false;
    }
  }

  $effect(() => {
    if (!listed) refresh();
  });

  async function run(fn: () => Promise<unknown>) {
    try {
      await fn();
    } catch (err) {
      onError(errorText(err));
    }
  }

  // Same reasoning as the volume slider: hold only the in-flight drag, and fall
  // back to whatever the backend has saved.
  let bufferDrag = $state<number | null>(null);
  let bufferFrames = $derived(bufferDrag ?? status.settings.captureBufferFrames);
</script>

<div class="stream">
  <div class="device">
    <label for="device">Capture device</label>
    <div class="row">
      <select
        id="device"
        bind:value={selected}
        disabled={streaming || devices.length === 0}
      >
        {#each devices as device (device.id)}
          <option value={device.id}>
            {device.name}{device.isLoopback ? "  (desktop audio)" : ""}
          </option>
        {/each}
        {#if devices.length === 0}
          <option value="">No capture devices found</option>
        {/if}
      </select>
      <button onclick={refresh} disabled={refreshing || streaming}>
        {refreshing ? "Scanning…" : "Refresh"}
      </button>
    </div>
  </div>

  {#if listed && !hasLoopback}
    <div class="callout">
      <strong>No desktop-audio device found.</strong>
      <p>
        Windows cannot record what your speakers are playing without a virtual audio
        cable, so a plain microphone is all that shows up here. Install
        <a href="https://vb-audio.com/Cable/" onclick={(e) => e.preventDefault()}>
          VB-Audio Virtual Cable
        </a>
        or VoiceMeeter, route the apps you want to share into it, then pick its
        output device above.
      </p>
    </div>
  {/if}

  <div class="transport">
    {#if streaming}
      <button class="danger" onclick={() => run(StopCapture)}>Stop streaming</button>
    {:else}
      <button
        class="primary"
        disabled={!status.inVoice || !selected}
        onclick={() => run(() => StartCapture(selected))}
      >
        Start streaming
      </button>
    {/if}
  </div>

  <div class="output">
    <VolumeSlider value={status.settings.volumePercent} disabled={!status.inVoice} />
    <LevelMeter rms={telemetry.rms} peak={telemetry.peak} active={streaming} />
  </div>

  <details>
    <summary>Buffering and stream health</summary>
    <div class="buffer">
      <label for="buffer">
        Buffer
        <span>{bufferFrames} frames · {bufferFrames * 20} ms</span>
      </label>
      <input
        id="buffer"
        type="range"
        min="3"
        max="15"
        step="1"
        value={bufferFrames}
        disabled={streaming}
        oninput={(e) => (bufferDrag = Number(e.currentTarget.value))}
        onchange={(e) => {
          bufferDrag = null;
          run(() => SetCaptureBufferFrames(Number(e.currentTarget.value)));
        }}
      />
      <p class="note">
        More buffer tolerates a busier machine; less is closer to live. Takes effect
        the next time you start streaming.
      </p>
    </div>

    <dl class="stats">
      <div><dt>Buffered</dt><dd>{telemetry.bufferedFrames} / {telemetry.bufferCapacity}</dd></div>
      <div><dt>Dropped</dt><dd class:bad={telemetry.droppedFrames > 0}>{telemetry.droppedFrames}</dd></div>
      <div><dt>Underruns</dt><dd class:bad={telemetry.underruns > 0}>{telemetry.underruns}</dd></div>
      <div><dt>Frames sent</dt><dd>{telemetry.framesSent}</dd></div>
      <div><dt>Clock resyncs</dt><dd class:bad={telemetry.resyncs > 0}>{telemetry.resyncs}</dd></div>
      <div><dt>Worst lateness</dt><dd>{telemetry.maxLatenessMs.toFixed(1)} ms</dd></div>
    </dl>
    <p class="note">
      Dropped frames mean audio arrived faster than it could be sent; underruns mean
      it arrived too slowly and silence was sent instead. A few of either around a
      start or stop is normal.
    </p>
  </details>

  {#if !status.inVoice}
    <p class="hint">Join a voice channel above to start streaming.</p>
  {/if}
</div>

<style>
  .stream {
    display: flex;
    flex-direction: column;
    gap: 20px;
  }

  label {
    display: block;
    color: var(--text-dim);
    font-size: 13px;
    margin-bottom: 6px;
  }

  .row {
    display: flex;
    gap: 8px;
  }

  .row button {
    flex: 0 0 auto;
  }

  .callout {
    padding: 12px 14px;
    background: color-mix(in srgb, var(--warn) 10%, var(--surface));
    border: 1px solid color-mix(in srgb, var(--warn) 40%, var(--border));
    border-radius: var(--radius-sm);
  }

  .callout p {
    margin: 6px 0 0;
    color: var(--text-dim);
    font-size: 13px;
  }

  .transport button {
    min-width: 140px;
  }

  .output {
    display: flex;
    flex-direction: column;
    gap: 12px;
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

  .buffer {
    margin: 14px 0;
  }

  .buffer label {
    display: flex;
    justify-content: space-between;
  }

  .buffer label span {
    font-variant-numeric: tabular-nums;
    color: var(--text-faint);
  }

  .stats {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
    gap: 10px 16px;
    margin: 14px 0 0;
  }

  .stats div {
    display: flex;
    justify-content: space-between;
    gap: 8px;
    padding-bottom: 4px;
    border-bottom: 1px solid var(--border);
  }

  dt {
    color: var(--text-faint);
    font-size: 12.5px;
  }

  dd {
    margin: 0;
    font-variant-numeric: tabular-nums;
    font-size: 12.5px;
  }

  dd.bad {
    color: var(--warn);
  }

  .note {
    margin: 8px 0 0;
    font-size: 12.5px;
    color: var(--text-faint);
  }

  .hint {
    margin: 0;
    color: var(--warn);
    font-size: 13px;
  }
</style>
