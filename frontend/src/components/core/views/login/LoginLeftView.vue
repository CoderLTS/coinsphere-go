<!-- 登录页品牌区沿用 Art Design Pro 的插画式构图。 -->
<template>
  <section class="login-left-view" :class="{ 'login-left-view--minimal': hideContent }">
    <div class="login-brand">
      <ArtLogo size="44" />
      <div>
        <strong>{{ AppConfig.systemInfo.name }}</strong>
        <span>Workflow · Strategy · Market</span>
      </div>
    </div>

    <div class="login-illustration" aria-hidden="true">
      <div class="illustration-halo"></div>
      <ThemeSvg :src="loginIcon" size="100%" />
    </div>

    <div class="login-copy">
      <h1>{{ $t('login.leftView.title') }}</h1>
      <p>{{ $t('login.leftView.subTitle') }}</p>
      <div class="login-features">
        <span><ArtSvgIcon icon="ri:flow-chart" /> {{ $t('login.features.workflow') }}</span>
        <span><ArtSvgIcon icon="ri:line-chart-line" /> {{ $t('login.features.signal') }}</span>
        <span><ArtSvgIcon icon="ri:notification-3-line" /> {{ $t('login.features.notify') }}</span>
      </div>
    </div>

    <span class="decoration decoration--ring" aria-hidden="true"></span>
    <span class="decoration decoration--square" aria-hidden="true"></span>
    <span class="decoration decoration--dot" aria-hidden="true"></span>
  </section>
</template>

<script setup lang="ts">
  import AppConfig from '@/config'
  import loginIcon from '@imgs/svg/login_icon.svg'

  withDefaults(
    defineProps<{
      hideContent?: boolean
    }>(),
    { hideContent: false }
  )
</script>

<style lang="scss" scoped>
  .login-left-view {
    position: relative;
    box-sizing: border-box;
    display: flex;
    flex: 0 0 60%;
    flex-direction: column;
    min-height: 100%;
    padding: 22px clamp(28px, 4vw, 64px) 42px;
    overflow: hidden;
    background: color-mix(in srgb, var(--el-color-primary-light-9) 82%, var(--default-bg-color));
  }

  .login-left-view::before {
    position: absolute;
    inset: 0;
    pointer-events: none;
    content: '';
    background-image: radial-gradient(var(--el-color-primary-light-7) 1px, transparent 1px);
    background-size: 24px 24px;
    opacity: 0.28;
    mask-image: linear-gradient(to bottom right, black, transparent 64%);
  }

  .login-left-view--minimal .login-illustration,
  .login-left-view--minimal .login-copy,
  .login-left-view--minimal .decoration {
    display: none;
  }

  .login-brand,
  .login-illustration,
  .login-copy {
    position: relative;
    z-index: 2;
  }

  .login-brand {
    display: flex;
    gap: 12px;
    align-items: center;
  }

  .login-brand strong,
  .login-brand span {
    display: block;
  }

  .login-brand strong {
    font-size: 20px;
    font-weight: 600;
    color: var(--art-gray-900);
  }

  .login-brand span {
    margin-top: 3px;
    font-size: 11px;
    color: var(--art-gray-600);
  }

  .login-illustration {
    width: min(48%, 440px);
    margin: auto auto 16px;
    animation: illustration-enter 0.65s ease both;
  }

  .illustration-halo {
    position: absolute;
    inset: 12%;
    z-index: -1;
    background: color-mix(in srgb, var(--theme-color) 14%, transparent);
    filter: blur(26px);
    border-radius: 50%;
  }

  .login-copy {
    width: min(100%, 620px);
    margin: 0 auto;
    text-align: center;
    animation: illustration-enter 0.65s 0.08s ease both;
  }

  .login-copy h1 {
    margin: 0;
    font-size: clamp(24px, 2.2vw, 34px);
    font-weight: 600;
    line-height: 1.28;
    color: var(--art-gray-900);
  }

  .login-copy p {
    max-width: 520px;
    margin: 12px auto 0;
    font-size: 14px;
    line-height: 1.7;
    color: var(--art-gray-600);
  }

  .login-features {
    display: flex;
    flex-wrap: wrap;
    gap: 10px;
    justify-content: center;
    margin-top: 24px;
  }

  .login-features span {
    display: inline-flex;
    gap: 7px;
    align-items: center;
    padding: 7px 11px;
    font-size: 12px;
    color: var(--art-gray-700);
    background: color-mix(in srgb, var(--default-box-color) 76%, transparent);
    border: 1px solid color-mix(in srgb, var(--theme-color) 12%, var(--default-border));
    border-radius: 8px;
  }

  .login-features :deep(.art-svg-icon) {
    font-size: 15px;
    color: var(--theme-color);
  }

  .decoration {
    position: absolute;
    z-index: 1;
    pointer-events: none;
  }

  .decoration--ring {
    top: 14%;
    left: 13%;
    width: 42px;
    height: 42px;
    border: 2px solid var(--el-color-primary-light-7);
    border-radius: 50%;
  }

  .decoration--square {
    right: 10%;
    bottom: 18%;
    width: 48px;
    height: 48px;
    background: var(--el-color-primary-light-8);
    border-radius: 12px;
    transform: rotate(28deg);
  }

  .decoration--dot {
    top: 23%;
    right: 18%;
    width: 14px;
    height: 14px;
    background: var(--el-color-primary-light-6);
    border-radius: 50%;
  }

  :global(html.dark) .login-left-view {
    background: color-mix(in srgb, var(--el-color-primary-light-9) 50%, var(--default-bg-color));
  }

  :global(html.dark) .login-features span {
    background: color-mix(in srgb, var(--default-box-color) 82%, transparent);
  }

  @keyframes illustration-enter {
    from {
      opacity: 0;
      transform: translateY(16px);
    }

    to {
      opacity: 1;
      transform: translateY(0);
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .login-illustration,
    .login-copy {
      animation: none;
    }
  }

  @media only screen and (max-width: 1180px) {
    .login-left-view {
      display: none;
    }
  }
</style>
