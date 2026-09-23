<script lang="ts">
  import { StopAllSounds } from "../../wailsjs/go/main/App";
  import type { main } from "../../wailsjs/go/models";
  import type { Telemetry } from "../lib/types";
  import { errorText } from "../lib/types";
  import LevelMeter from "./LevelMeter.svelte";
  import SoundboardTrack from "./SoundboardTrack.svelte";
  import StreamHealth from "./StreamHealth.svelte";
  import VolumeSlider from "./VolumeSlider.svelte";

  let {
    status,
    telemetry,
    soundboard,
    onError,
  }: {
    status: main.Status;
    telemetry: Telemetry;
    soundboard: main.SoundboardState;
    onError: (message: string) => void;
  } = $props();

  let active = $derived(status.mode === "soundboard");
  let anyPlaying = $derived(soundboard.tracks.some((t) => t.path !== ""));

  async function stopAll() {
    try {
      await StopAllSounds();
    } catch (err) {
      onError(errorText(err));
    }
  }
</script>

<div class="soundboard">
  {#if !status.inVoice}
    <p class="hint">Join a voice channel above to start playing.</p>
  {:else if !active && status.mode !== "idle"}
    <p class="hint">
      Playing a sound here stops the {status.mode === "capture" ? "desktop stream" : "playlist"}.
    </p>
  {/if}

  <div class="tracks">
    {#each soundboard.tracks as track, i (i)}
      <SoundboardTrack
        index={i}
        {track}
        stats={telemetry.soundboard?.[i]}
        inVoice={status.inVoice}
        {onError}
      />
    {/each}
  </div>

  <div class="master">
    <div class="output">
      <VolumeSlider
        label="Master"
        value={status.settings.volumePercent}
        disabled={!status.inVoice}
      />
      <LevelMeter rms={telemetry.rms} peak={telemetry.peak} active={active && anyPlaying} />
    </div>
    <button class="danger" disabled={!anyPlaying} onclick={stopAll}>Stop all</button>
  </div>

  <details>
    <summary>Stream health</summary>
    <StreamHealth {telemetry} live={false} />
  </details>
</div>

<style>
  .soundboard {
    display: flex;
    flex-direction: column;
    gap: 16px;
  }

  /* Two tracks a row fits the default window; a narrow one stacks them. */
  .tracks {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(380px, 1fr));
    gap: 14px;
  }

  .master {
    display: flex;
    align-items: center;
    gap: 16px;
  }

  .output {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 10px;
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
