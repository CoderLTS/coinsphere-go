// scripts/clean-dev.ts
import fs from 'fs/promises'
import path from 'path'

// 清理语言文件
async function cleanLanguageFiles() {
  const languageFiles = [
    { path: 'src/locales/langs/zh.json', name: '中文语言文件' },
    { path: 'src/locales/langs/en.json', name: '英文语言文件' }
  ]

  for (const { path: langPath, name } of languageFiles) {
    try {
      const fullPath = path.resolve(process.cwd(), langPath)
      const content = await fs.readFile(fullPath, 'utf-8')
      const langData = JSON.parse(content)

      const menusToRemove = [
        'widgets',
        'template',
        'article',
        'examples',
        'safeguard',
        'plan',
        'help'
      ]

      if (langData.menus) {
        menusToRemove.forEach((menuKey) => {
          if (langData.menus[menuKey]) {
            delete langData.menus[menuKey]
          }
        })

        if (langData.menus.dashboard) {
          if (langData.menus.dashboard.analysis) {
            delete langData.menus.dashboard.analysis
          }
          if (langData.menus.dashboard.ecommerce) {
            delete langData.menus.dashboard.ecommerce
          }
        }

        if (langData.menus.system) {
          const systemKeysToRemove = [
            'nested',
            'menu1',
            'menu2',
            'menu21',
            'menu3',
            'menu31',
            'menu32',
            'menu321'
          ]
          systemKeysToRemove.forEach((key) => {
            if (langData.menus.system[key]) {
              delete langData.menus.system[key]
            }
          })
        }
      }

      await fs.writeFile(fullPath, JSON.stringify(langData, null, 2), 'utf-8')
      console.log(`     ${icons.success} ${fmt.success(`清理${name}完成`)}`)
    } catch (err) {
      console.log(`     ${icons.error} ${fmt.error(`清理${name}失败`)}`)
      console.log(`     ${fmt.dim('错误详情: ' + err)}`)
    }
  }
}

// 清理快速入口组件
async function cleanFastEnterComponent() {
  const fastEnterPath = path.resolve(process.cwd(), 'src/config/fastEnter.ts')

  try {
    const cleanedFastEnter = `/**
 * 快速入口配置
 * 包含：应用列表、快速链接等配置
 */
import { WEB_LINKS } from '@/utils/constants'
import type { FastEnterConfig } from '@/types/config'

const fastEnterConfig: FastEnterConfig = {
  // 显示条件（屏幕宽度）
  minWidth: 1200,
  // 应用列表
  applications: [
    {
      name: '工作台',
      description: '系统概览与数据统计',
      icon: 'ri:pie-chart-line',
      iconColor: '#377dff',
      enabled: true,
      order: 1,
      routeName: 'Console'
    },
    {
      name: '官方文档',
      description: '使用指南与开发文档',
      icon: 'ri:bill-line',
      iconColor: '#ffb100',
      enabled: true,
      order: 2,
      link: WEB_LINKS.DOCS
    },
    {
      name: '技术支持',
      description: '技术支持与问题反馈',
      icon: 'ri:user-location-line',
      iconColor: '#ff6b6b',
      enabled: true,
      order: 3,
      link: WEB_LINKS.COMMUNITY
    },
    {
      name: '哔哩哔哩',
      description: '技术分享与交流',
      icon: 'ri:bilibili-line',
      iconColor: '#FB7299',
      enabled: true,
      order: 4,
      link: WEB_LINKS.BILIBILI
    }
  ],
  // 快速链接
  quickLinks: [
    {
      name: '登录',
      enabled: true,
      order: 1,
      routeName: 'Login'
    },
    {
      name: '注册',
      enabled: true,
      order: 2,
      routeName: 'Register'
    },
    {
      name: '忘记密码',
      enabled: true,
      order: 3,
      routeName: 'ForgetPassword'
    },
    {
      name: '个人中心',
      enabled: true,
      order: 4,
      routeName: 'UserCenter'
    }
  ]
}

export default Object.freeze(fastEnterConfig)
`

    await fs.writeFile(fastEnterPath, cleanedFastEnter, 'utf-8')
    console.log(`     ${icons.success} ${fmt.success('清理快速入口配置完成')}`)
  } catch (err) {
    console.log(`     ${icons.error} ${fmt.error('清理快速入口配置失败')}`)
    console.log(`     ${fmt.dim('错误详情: ' + err)}`)
  }
}

// 更新菜单接口
async function updateMenuApi() {
  const apiPath = path.resolve(process.cwd(), 'src/api/system-manage.ts')

  try {
    const content = await fs.readFile(apiPath, 'utf-8')
    const updatedContent = content.replace(
      "url: '/api/v3/system/menus'",
      "url: '/api/v3/system/menus/simple'"
    )

    await fs.writeFile(apiPath, updatedContent, 'utf-8')
    console.log(`     ${icons.success} ${fmt.success('更新菜单接口完成')}`)
  } catch (err) {
    console.log(`     ${icons.error} ${fmt.error('更新菜单接口失败')}`)
    console.log(`     ${fmt.dim('错误详情: ' + err)}`)
  }
}

// 用户确认函数
async function getUserConfirmation(): Promise<boolean> {
  const { createInterface } = await import('readline')

  return new Promise((resolve) => {
    const rl = createInterface({
      input: process.stdin,
      output: process.stdout
    })

    console.log(
      `  ${fmt.highlight('请输入')} ${fmt.success('yes')} ${fmt.highlight('确认执行清理操作，或按 Enter 取消')}`
    )
    console.log()
    process.stdout.write(`  ${icons.arrow} `)

    rl.question('', (answer: string) => {
      rl.close()
      resolve(answer.toLowerCase().trim() === 'yes')
    })
  })
}

// 显示清理警告
async function showCleanupWarning() {
  createCard('安全警告', [
    `${fmt.warning('此操作将永久删除以下演示内容，且无法恢复！')}`,
    `${fmt.dim('请仔细阅读清理列表，确认后再继续操作')}`
  ])

  const cleanupItems = [
    {
      icon: icons.image,
      name: '图片资源',
      desc: '演示用的封面图片、3D图片、运维图片等',
      color: theme.orange
    },
    {
      icon: icons.file,
      name: '演示页面',
      desc: 'widgets、template、article、examples、safeguard等页面',
      color: theme.purple
    },
    {
      icon: icons.code,
      name: '路由模块文件',
      desc: '删除演示路由模块，只保留核心模块（dashboard、system、result、exception）',
      color: theme.primary
    },
    {
      icon: icons.link,
      name: '路由别名',
      desc: '重写routesAlias.ts，移除演示路由别名',
      color: theme.info
    },
    {
      icon: icons.data,
      name: 'Mock数据',
      desc: '演示用的JSON数据、文章列表、评论数据等',
      color: theme.success
    },
    {
      icon: icons.globe,
      name: '多语言文件',
      desc: '清理中英文语言包中的演示菜单项',
      color: theme.warning
    },
    { icon: icons.map, name: '地图组件', desc: '移除art-map-chart地图组件', color: theme.error },
    { icon: icons.chat, name: '评论组件', desc: '移除comment-widget评论组件', color: theme.orange },
    {
      icon: icons.bolt,
      name: '快速入口',
      desc: '移除分析页、礼花效果、聊天、更新日志、定价、留言管理等无效项目',
      color: theme.purple
    }
  ]

  console.log(`  ${fmt.badge('', theme.bgRed)} ${fmt.title('将要清理的内容')}`)
  console.log()

  cleanupItems.forEach((item, index) => {
    console.log(`     ${item.color}${theme.reset} ${fmt.highlight(`${index + 1}. ${item.name}`)}`)
    console.log(`        ${fmt.dim(item.desc)}`)
  })

  console.log()
  console.log(`  ${fmt.badge('', theme.bgGreen)} ${fmt.title('保留的功能模块')}`)
  console.log()

  const preservedModules = [
    { name: 'Dashboard', desc: '工作台页面' },
    { name: 'System', desc: '系统管理模块' },
    { name: 'Result', desc: '结果页面' },
    { name: 'Exception', desc: '异常页面' },
    { name: 'Auth', desc: '登录注册功能' },
    { name: 'Core Components', desc: '核心组件库' }
  ]

  preservedModules.forEach((module) => {
    console.log(`     ${icons.check} ${fmt.success(module.name)} ${fmt.dim(`- ${module.desc}`)}`)
  })

  console.log()
  createDivider()
  console.log()
}

// 显示统计信息
async function showStats() {
  const duration = Date.now() - stats.startTime
  const seconds = (duration / 1000).toFixed(2)

  console.log()
  createCard('清理统计', [
    `${fmt.success('成功删除')}: ${fmt.highlight(stats.deletedFiles.toString())} 个文件`,
    `${fmt.info('涉及路径')}: ${fmt.highlight(stats.deletedPaths.toString())} 个目录/文件`,
    ...(stats.failedPaths > 0
      ? [
          `${icons.error} ${fmt.error('删除失败')}: ${fmt.highlight(stats.failedPaths.toString())} 个路径`
        ]
      : []),
    `${fmt.info('耗时')}: ${fmt.highlight(seconds)} 秒`
  ])
}

// 创建成功横幅
function createSuccessBanner() {
  console.log()
  console.log(
    fmt.gradient('  ╔══════════════════════════════════════════════════════════════════╗')
  )
  console.log(
    fmt.gradient('  ║                                                                  ║')
  )
  console.log(
    `  ║                  ${icons.star} ${fmt.success('清理完成！项目已准备就绪')} ${icons.rocket}                  ║`
  )
  console.log(
    `  ║                    ${fmt.dim('现在可以开始您的开发之旅了！')}                  ║`
  )
  console.log(
    fmt.gradient('  ║                                                                  ║')
  )
  console.log(
    fmt.gradient('  ╚══════════════════════════════════════════════════════════════════╝')
  )
  console.log()
}

// 主函数
async function main() {
  // 清屏并显示横幅
  console.clear()
  createModernBanner()

  // 显示清理警告
  await showCleanupWarning()

  // 统计文件数量
  console.log(`  ${fmt.info('正在统计文件数量...')}`)
  stats.totalFiles = await countAllFiles()

  console.log(`  ${fmt.info('即将清理')}: ${fmt.highlight(stats.totalFiles.toString())} 个文件`)
  console.log(`  ${fmt.dim(`涉及 ${targets.length} 个目录/文件路径`)}`)
  console.log()

  // 用户确认
  const confirmed = await getUserConfirmation()

  if (!confirmed) {
    console.log(`  ${fmt.warning('操作已取消，清理中止')}`)
    console.log()
    return
  }

  console.log()
  console.log(`  ${icons.check} ${fmt.success('确认成功，开始清理...')}`)
  console.log()

  // 开始清理过程
  console.log(`  ${fmt.badge('步骤 1/6', theme.bgBlue)} ${fmt.title('删除演示文件')}`)
  console.log()
  for (let i = 0; i < targets.length; i++) {
    await remove(targets[i], i)
  }
  console.log()

  console.log(`  ${fmt.badge('步骤 2/6', theme.bgBlue)} ${fmt.title('清理路由模块')}`)
  console.log()
  await cleanRouteModules()
  console.log()

  console.log(`  ${fmt.badge('步骤 3/6', theme.bgBlue)} ${fmt.title('重写路由别名')}`)
  console.log()
  await cleanRoutesAlias()
  console.log()

  console.log(`  ${fmt.badge('步骤 4/6', theme.bgBlue)} ${fmt.title('清理语言文件')}`)
  console.log()
  await cleanLanguageFiles()
  console.log()

  console.log(`  ${fmt.badge('步骤 5/6', theme.bgBlue)} ${fmt.title('清理快速入口')}`)
  console.log()
  await cleanFastEnterComponent()
  console.log()

  console.log(`  ${fmt.badge('步骤 6/6', theme.bgBlue)} ${fmt.title('更新菜单接口')}`)
  console.log()
  await updateMenuApi()

  // 显示统计信息
  await showStats()

  // 显示成功横幅
  createSuccessBanner()
}

main().catch((err) => {
  console.log()
  console.log(`  ${icons.error} ${fmt.error('清理脚本执行出错')}`)
  console.log(`  ${fmt.dim('错误详情: ' + err)}`)
  console.log()
  process.exit(1)
})
