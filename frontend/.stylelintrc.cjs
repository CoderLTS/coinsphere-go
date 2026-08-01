module.exports = {
  // 继承推荐规范配置
  extends: [
    'stylelint-config-standard',
    'stylelint-config-recommended-scss',
    'stylelint-config-recommended-vue/scss',
    'stylelint-config-html/vue',
    'stylelint-config-recess-order'
  ],
  // 指定不同文件对应的解析器
  overrides: [
    {
      files: ['**/*.{vue,html}'],
      customSyntax: 'postcss-html'
    },
    {
      files: ['**/*.{css,scss}'],
      customSyntax: 'postcss-scss'
    },
    {
      // GitHub Markdown 样式按级联阶段分段覆盖同一选择器，不能机械合并。
      files: ['src/assets/styles/core/md.scss'],
      rules: {
        'no-duplicate-selectors': null
      }
    },
    {
      // 该兼容性 mixin 显式保留旧浏览器前缀。
      files: ['src/assets/styles/core/mixin.scss'],
      rules: {
        'at-rule-no-vendor-prefix': null,
        'selector-no-vendor-prefix': null
      }
    }
  ],
  // 自定义规则
  rules: {
    'import-notation': 'string', // 指定导入CSS文件的方式("string"|"url")
    'alpha-value-notation': 'number', // 透明度统一使用 0.x 数值
    'color-function-notation': 'modern', // 颜色函数统一使用现代空格与斜杠语法
    // 同时允许前缀与范围语法，避免 Stylelint 自动修复改变既有浏览器兼容边界。
    'media-feature-range-notation': null,
    'at-rule-empty-line-before': [
      'always',
      {
        except: ['blockless-after-same-name-blockless', 'first-nested'],
        ignore: ['after-comment'],
        ignoreAtRules: ['else'] // Prettier 将 SCSS 的 } @else 保持在同一行
      }
    ],
    'selector-class-pattern': null, // 选择器类名命名规则
    'custom-property-pattern': null, // 自定义属性命名规则
    'keyframes-name-pattern': null, // 动画帧节点样式命名规则
    'no-descending-specificity': null, // 允许无降序特异性
    'no-empty-source': null, // 允许空样式
    'property-no-vendor-prefix': null, // 允许属性前缀
    // 允许 global 、export 、deep伪类
    'selector-pseudo-class-no-unknown': [
      true,
      {
        ignorePseudoClasses: ['global', 'export', 'deep']
      }
    ],
    // 允许未知属性
    'property-no-unknown': [
      true,
      {
        ignoreProperties: []
      }
    ],
    // 允许未知规则
    'at-rule-no-unknown': [
      true,
      {
        ignoreAtRules: [
          'apply',
          'custom-variant',
          'forward',
          'function',
          'use',
          'mixin',
          'include',
          'extend',
          'each',
          'if',
          'else',
          'for',
          'while',
          'reference',
          'return',
          'theme',
          'utility'
        ]
      }
    ],
    'scss/at-rule-no-unknown': [
      true,
      {
        ignoreAtRules: [
          'apply',
          'custom-variant',
          'forward',
          'function',
          'use',
          'mixin',
          'include',
          'extend',
          'each',
          'if',
          'else',
          'for',
          'while',
          'reference',
          'return',
          'theme',
          'utility'
        ]
      }
    ]
  }
}
