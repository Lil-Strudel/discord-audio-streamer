<script lang="ts">
  /**
   * A level meter of what is actually being sent, after the volume slider.
   * RMS drives the filled bar; the decaying peak is drawn as a separate marker,
   * because a raw per-frame peak at fifty frames a second is unreadable.
   */
  let { rms = 0, peak = 0, active = false } = $props();

  // Levels are linear amplitude, but hearing is not. A cube root spreads the
  // quiet end of the scale out so ordinary music does not sit pinned near zero.
  const scale = (v: number) => Math.min(1, Math.max(0, Math.cbrt(v)));

  let rmsPct = $derived(active ? scale(rms) * 100 : 0);
  let peakPct = $derived(active ? scale(peak) * 100 : 0);
  let clipping = $derived(peak >= 0.999);
</script>

<div class="meter" class:active>
  <div class="fill" style="width: {rmsPct}%"></div>
  {#if peakPct > 0}
    <div class="peak" class:clipping style="left: {peakPct}%"></div>
  {/if}
</div>

<style>
  .meter {
    position: relative;
    height: 8px;
    border-radius: 4px;
    background: var(--bg);
    border: 1px solid var(--border);
    overflow: hidden;
  }

  .fill {
    height: 100%;
    /* Green through most of the range, warning as it approaches full scale. */
    background: linear-gradient(
      to right,
      var(--good) 0%,
      var(--good) 70%,
      var(--warn) 88%,
      var(--bad) 100%
    );
    background-size: 100vw 100%;
    transition: width 80ms linear;
  }

  .peak {
    position: absolute;
    top: 0;
    bottom: 0;
    width: 2px;
    margin-left: -1px;
    background: var(--text);
    transition: left 80ms linear;
  }

  .peak.clipping {
    background: var(--bad);
    width: 3px;
  }
</style>
