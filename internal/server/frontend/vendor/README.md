# 内嵌前端依赖

本目录随 Go `embed` 编入 bincat 二进制。部署后浏览器从 `/vendor/` 加载，无需访问 CDN。文件版本从现有产物头部确认；本次发布未升级依赖。

| 文件 | 上游包/版本 | 许可证 |
| --- | --- | --- |
| vue.global.prod.js | vue 3.5.44 | LICENSE.vue |
| element-plus.js、element-plus.css、element-plus-locale-zh-cn.js | element-plus 2.14.7 | LICENSE.element-plus |
| element-plus-icons.js | @element-plus/icons-vue 2.3.2 | LICENSE.element-plus-icons |

上游：[Vue](https://github.com/vuejs/core)、[Element Plus](https://github.com/element-plus/element-plus)、[Element Plus Icons](https://github.com/element-plus/element-plus-icons)。对应许可证从固定版本包/上游标签取得，详见本目录 LICENSE 文件。

升级时应使用固定版本，保留版权和许可，核对浏览器全局变量 Vue / ElementPlus / ElementPlusLocaleZhCn / ElementPlusIconsVue，然后验证登录页、菜单和提示交互并重新构建。不要以 latest 地址替换现有版本。

当前静态资源 SHA-256 见 SHA256SUMS，可在本目录用 `sha256sum -c SHA256SUMS`（Linux）或 `shasum -a 256 -c SHA256SUMS`（macOS）检查。
