<script lang="ts">
  import {
    ListCaptureDevices, SetCaptureBufferFrames, StartCapture, StopCapture,
  } from "../../wailsjs/go/main/App";
  import type { ffmpeg, main } from "../../wailsjs/go/models";
  import type { Telemetry } from "../lib/types";
  import { errorText } from "../lib/types";
  import LevelMeter from "./LevelMeter.svelte";
  import StreamHealth from "./StreamHealth.svelte";
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
  let outputs = $derived(devices.filter((d) => d.isOutput));
  let inputs = $derived(devices.filter((d) => !d.isOutput));

  async function refresh() {
    refreshing = true;
    try {
      devices = (await ListCaptureDevices()) ?? [];
      listed = true;
      if (!devices.some((d) => d.id === selected)) {
        // Streaming desktop audio is what this screen is for, so the default
        // speakers are the best guess when there is nothing remembered.
        const remembered = devices.find((d) => d.id === status.settings.lastCaptureDeviceId);
        selected =
          (remembered ??
            devices.find((d) => d.isOutput && d.isDefault) ??
            devices.find((d) => d.isOutput) ??
            devices[0])?.id ?? "";
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
        {#if outputs.length}
          <optgroup label="Outputs — what you hear">
            {#each outputs as device (device.id)}
              <option value={device.id}>
                {device.name}{device.isDefault ? "  (default)" : ""}
              </option>
            {/each}
          </optgroup>
        {/if}
        {#if inputs.length}
          <optgroup label="Inputs — microphones and virtual cables">
            {#each inputs as device (device.id)}
              <option value={device.id}>
                {device.name}{device.isDefault ? "  (default)" : ""}{device.isLoopback
                  ? "  (desktop audio)"
                  : ""}
              </option>
            {/each}
          </optgroup>
        {/if}
        {#if devices.length === 0}
          <option value="">No audio devices found</option>
        {/if}
      </select>
      <button onclick={refresh} disabled={refreshing || streaming}>
        {refreshing ? "Scanning…" : "Refresh"}
      </button>
    </div>
    <p class="note">
      Pick an output to stream whatever this machine is playing through it, or an
      input to stream a microphone. No virtual audio cable is needed.
    </p>
  </div>

  {#if listed && devices.length === 0}
    <div class="callout">
      <strong>No audio devices found.</strong>
      <p>
        Windows reported no active playback or recording devices at all. Check that
        your speakers or headphones are connected and enabled in Sound settings,
        then press Refresh.
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

    <StreamHealth {telemetry} live />
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
