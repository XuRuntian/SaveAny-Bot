---
title: "Watch Chats"
weight: 4
---

# Watch Chats

Use `/watch https://t.me/c/123456789/520482` to watch that message's sender
in the source chat without a username. Explicit filters override the inferred sender.
Inaccessible messages or missing user IDs return an error. Use the same arguments
with `/unwatch` to remove the watch. Automatic Telegram saves append
`#userid_<ID>`; anonymous and channel identities do not receive a user tag.

{{< hint warning >}}
This feature requires enabling UserBot integration.
{{< /hint >}}

You can watch messages in a specific chat and automatically save them to the default storage, following storage rules. You can also add filters so that only matching messages are saved.

Original media groups are saved as batch tasks. If the target is Telegram storage, the bot will try to preserve photos/videos as album messages.

Loose photos/videos from the same sender are not grouped by default. Enable soft grouping explicitly if you want to organize consecutive loose media messages.

Watch a chat:

```
/watch <chat_id/username/message_link> [filter] [group:soft] [window:8s] [max:10]
```

Stop watching:

```
/unwatch <chat_id/username/message_link> [filter] [group:soft] [window:8s] [max:10]
```

Filter types:

## msgre

Regex-match the message text. For example:

```
/watch 12345678 msgre:.*hello.*
```

This will watch the chat with ID `12345678`, and only save messages whose text contains `hello`.

## from

Match the message sender by user ID or username. Separate multiple users with commas. Usernames are resolved to numeric IDs when the watch is added. For example:

```
/watch 12345678 from:@Luscious_Yana,@another_user,123456789
```

This will watch the chat with ID `12345678`, and only save media messages sent by those users.

## soft group

Group consecutive loose photos/videos from the same source chat, sender, and watching user within a time window. For example:

```
/watch 12345678 from:@Luscious_Yana group:soft window:8s max:10
```

This buffers loose media from `@Luscious_Yana` for 8 seconds. If another matching media message arrives within that window, the window is extended; once 10 media items are buffered, the group is flushed immediately. Original Telegram media groups are still handled by their original group first.
