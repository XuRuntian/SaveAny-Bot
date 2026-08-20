---
title: "监听聊天"
weight: 4
---

# 监听聊天

{{< hint warning >}}
该功能需开启 UserBot 集成.
{{< /hint >}}

监听指定聊天的消息, 并自动保存到默认存储中, 遵从存储规则, 并且可以设置过滤器来只保存匹配的消息.

监听到原始媒体组时, 会作为批量任务保存. 如果目标是 Telegram 存储, Bot 会尽量将图片/视频保留为相册消息.

默认不会把同一发送者的散图自动合并. 如需整理连续发送的散图/视频, 可以显式启用软分组.

监听聊天:

```
/watch <chat_id/username> [filter] [group:soft] [window:8s] [max:10]
```

取消监听:

```
/unwatch <chat_id/username>
```

过滤器类型:

## msgre

正则匹配消息文本, 例如:

```
/watch 12345678 msgre:.*hello.*
```

这将会监听 ID 为 12345678 的聊天, 并且只保存消息文本中包含 "hello" 的消息.

## from

匹配消息发送者, 可以使用用户 ID 或用户名. 多个用户用逗号分隔. 用户名会在添加监听时解析为数字 ID 并保存, 例如:

```
/watch 12345678 from:@Luscious_Yana,@another_user,123456789
```

这将会监听 ID 为 12345678 的聊天, 并且只保存来自这些用户的媒体消息.

## soft group

将同一源聊天、同一发送者、同一监听用户下连续出现的散图/视频按时间窗口合并, 例如:

```
/watch 12345678 from:@Luscious_Yana group:soft window:8s max:10
```

这会把来自 `@Luscious_Yana` 的散图/视频先缓存 8 秒. 如果 8 秒内继续出现同一发送者的新媒体, 会延后出组; 达到 10 个媒体会立即出组. 原始 Telegram 媒体组仍优先按原组处理.
