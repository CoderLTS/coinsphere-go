/** 前端配置模块：fastEnter。 */
/**
 * 模块化快速入口配置。
 */
import { WEB_LINKS } from '@/utils/constants'
import type { FastEnterConfig } from '@/types/config'

const fastEnterConfig: FastEnterConfig = {
  minWidth: 1200,
  applications: [
    {
      name: '首页',
      description: '查看首页运行态势与关键摘要',
      icon: 'ri:home-5-line',
      iconColor: '#377dff',
      enabled: true,
      order: 1,
      routeName: 'Dashboard'
    },
    {
      name: '任务概览',
      description: '查看定时任务统计分析',
      icon: 'ri:line-chart-line',
      iconColor: '#11a36a',
      enabled: true,
      order: 2,
      routeName: 'SchedulerOverview'
    },
    {
      name: '新闻数据',
      description: '进入数据管理维护新闻数据',
      icon: 'ri:database-2-line',
      iconColor: '#ff8a00',
      enabled: true,
      order: 3,
      routeName: 'NewsData'
    },
    {
      name: '官方文档',
      description: '查看使用指南与项目文档',
      icon: 'ri:bill-line',
      iconColor: '#8b5cf6',
      enabled: true,
      order: 4,
      link: WEB_LINKS.DOCS
    }
  ],
  quickLinks: [
    {
      name: '登录',
      enabled: true,
      order: 1,
      routeName: 'Login'
    },
    {
      name: '个人中心',
      enabled: true,
      order: 2,
      routeName: 'UserCenter'
    }
  ]
}

export default Object.freeze(fastEnterConfig)
