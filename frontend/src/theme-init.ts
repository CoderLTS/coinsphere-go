// 在应用挂载前恢复主题，避免刷新时先显示错误的背景色。
try {
  if (localStorage.getItem('sys-theme') === 'dark') {
    document.documentElement.classList.add('dark')
  }
} catch (error) {
  console.warn('Failed to apply initial theme:', error)
}
