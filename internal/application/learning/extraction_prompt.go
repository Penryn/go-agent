package learning

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

// ExtractionPromptBuilder 构建记忆提炼 Prompt
type ExtractionPromptBuilder struct {
	extractorVersion string
}

// NewExtractionPromptBuilder 创建 Prompt 构建器
func NewExtractionPromptBuilder(version string) *ExtractionPromptBuilder {
	return &ExtractionPromptBuilder{
		extractorVersion: version,
	}
}

// BuildPrompt 构建记忆提炼 Prompt
func (b *ExtractionPromptBuilder) BuildPrompt(window *Window) string {
	var sections []string

	// 1. 任务说明
	sections = append(sections, `# 记忆提炼任务

你的任务是从一段群聊对话中提取值得长期记住的信息，并以结构化格式输出。

## 提取规则

### 应该提取的内容：
1. **用户偏好和习惯** - 用户明确表达的喜好、讨厌的事物、日常习惯
2. **互动边界** - 用户明确要求的称呼方式、是否允许 @ 或戳一戳
3. **有意义的共同经历** - 群内发生的重要事件、决定、讨论结果
4. **群文化和梗** - 群内特有的梗、暗号、共同认知的来源
5. **用户背景信息** - 工作、学校、兴趣爱好等稳定信息

### 不应该提取的内容：
- 短暂的情绪波动（"今天好累"）
- 日常寒暄和客套话
- 纯粹的刷屏和重复
- 临时的计划安排（"明天见"）
- 每条消息都记录（过度记忆）

## 输出格式

以 JSON 数组格式输出，每个记忆候选包含以下字段：

`)

	// 2. JSON Schema
	sections = append(sections, `### 记忆候选格式
`+"```json"+`
[
  {
    "subject_kind": "user | group",
    "subject_id": "用户ID或群ID",
    "type": "semantic | social | episodic",
    "subtype": "具体子类型",
    "content": "自然语言描述",
    "predicate": "结构化谓词（可选）",
    "normalized_value": "归一化值（可选）",
    "qualifier": "限定词（可选）",
    "valid_until": "过期时间ISO8601（可选）",
    "participant_ids": ["参与者ID列表"],
    "bot_role": "participant | observer",
    "anchor_event_id": "锚点事件ID",
    "evidence_event_ids": ["证据事件ID列表"],
    "confidence": 0.0-1.0,
    "reasoning": "提取理由"
  }
]
`+"```"+`

### 字段说明

- **subject_kind**:
  - "user": 用户级记忆（个人偏好、习惯）
  - "group": 群级记忆（共同经历、群文化）

- **type**:
  - "semantic": 语义记忆（事实、偏好、知识）
  - "social": 社交记忆（称呼、边界、关系）
  - "episodic": 情景记忆（事件、经历）

- **subtype**:
  - semantic: preference, habit, background, skill
  - social: nickname, boundary, relationship
  - episodic: discussion, decision, event, meme_origin

- **predicate** (结构化谓词，用于约束类记忆):
  - likes_topic, dislikes_topic
  - preferred_name, allow_mention, allow_poke
  - participated_in, witnessed

- **qualifier**: 限定词（如时间范围、条件）
  - "default": 无限定
  - "ISO8601/ISO8601": 时间范围
  - "when_xxx": 条件限定

- **confidence**:
  - 0.9-1.0: 明确表达
  - 0.7-0.9: 强暗示
  - 0.5-0.7: 推断
  - < 0.5: 不确定，不应提取

`)

	// 3. 对话内容
	sections = append(sections, fmt.Sprintf("\n## 待分析对话\n\n群ID: %d\n时间范围: %s 到 %s\n\n",
		window.GroupID,
		window.StartTime.Format("2006-01-02 15:04:05"),
		window.EndTime.Format("2006-01-02 15:04:05")))

	// 格式化对话
	for i, event := range window.Events {
		timestamp := time.Unix(event.TimestampUnix, 0).Format("15:04:05")
		userName := event.Sender.DisplayName
		if userName == "" {
			userName = fmt.Sprintf("User%d", event.UserID)
		}

		var content string
		// 简化处理：直接使用 Text 字段
		if event.Text != "" {
			content = event.Text
		} else {
			// 根据 Segments 判断类型
			if len(event.Segments) > 0 {
				segType := event.Segments[0].Type
				switch segType {
				case "image":
					content = "[图片]"
				case "face":
					content = "[表情]"
				case "file":
					content = "[文件]"
				default:
					content = fmt.Sprintf("[%s]", segType)
				}
			} else {
				content = "[消息]"
			}
		}

		sections = append(sections, fmt.Sprintf("[%d] %s @%s(ID:%d): %s",
			i, timestamp, userName, event.UserID, content))
	}

	// 4. 输出要求
	sections = append(sections, `

## 输出要求

1. 仅输出 JSON 数组，不要有任何额外说明
2. 确保 JSON 格式正确，可以被解析
3. confidence < 0.7 的候选不要输出
4. 如果没有值得记录的内容，输出空数组 []
5. evidence_event_ids 必须是上面对话中的真实序号

## 示例

输入：
[0] 10:23:45 @张三(ID:123): 我最喜欢喝乌龙茶了
[1] 10:23:50 @李四(ID:456): 我也是！
[2] 10:24:00 @张三(ID:123): 以后别 @ 我了，直接私聊

输出：
`+"```json"+`
[
  {
    "subject_kind": "user",
    "subject_id": "123",
    "type": "semantic",
    "subtype": "preference",
    "content": "张三喜欢喝乌龙茶",
    "predicate": "likes_topic",
    "normalized_value": "乌龙茶",
    "qualifier": "default",
    "participant_ids": ["123"],
    "bot_role": "observer",
    "anchor_event_id": "0",
    "evidence_event_ids": ["0"],
    "confidence": 0.95,
    "reasoning": "用户明确表达了对乌龙茶的喜好"
  },
  {
    "subject_kind": "user",
    "subject_id": "123",
    "type": "social",
    "subtype": "boundary",
    "content": "张三要求不要 @ 他，直接私聊",
    "predicate": "allow_mention",
    "normalized_value": "false",
    "qualifier": "default",
    "participant_ids": ["123"],
    "bot_role": "observer",
    "anchor_event_id": "2",
    "evidence_event_ids": ["2"],
    "confidence": 0.98,
    "reasoning": "用户明确表达了互动边界"
  }
]
`+"```"+`

现在请分析上面的对话，提取记忆候选。
`)

	return strings.Join(sections, "\n")
}

// ParseExtractionResponse 解析 LLM 的提炼响应
func (b *ExtractionPromptBuilder) ParseExtractionResponse(response string, window *Window) ([]*memorydomain.MemoryCandidate, error) {
	// 清理响应（移除可能的 markdown 代码块标记）
	response = strings.TrimSpace(response)
	response = strings.TrimPrefix(response, "```json")
	response = strings.TrimPrefix(response, "```")
	response = strings.TrimSuffix(response, "```")
	response = strings.TrimSpace(response)

	// 如果是空数组，直接返回
	if response == "[]" || response == "" {
		return []*memorydomain.MemoryCandidate{}, nil
	}

	// 解析 JSON
	var rawCandidates []struct {
		SubjectKind      string    `json:"subject_kind"`
		SubjectID        string    `json:"subject_id"`
		Type             string    `json:"type"`
		Subtype          string    `json:"subtype"`
		Content          string    `json:"content"`
		Predicate        string    `json:"predicate"`
		NormalizedValue  string    `json:"normalized_value"`
		Qualifier        string    `json:"qualifier"`
		ValidUntil       *string   `json:"valid_until"`
		ParticipantIDs   []string  `json:"participant_ids"`
		BotRole          string    `json:"bot_role"`
		AnchorEventID    string    `json:"anchor_event_id"`
		EvidenceEventIDs []string  `json:"evidence_event_ids"`
		Confidence       float64   `json:"confidence"`
		Reasoning        string    `json:"reasoning"`
	}

	if err := json.Unmarshal([]byte(response), &rawCandidates); err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %w\nResponse: %s", err, response)
	}

	// 转换为 MemoryCandidate
	candidates := make([]*memorydomain.MemoryCandidate, 0, len(rawCandidates))
	scope := fmt.Sprintf("group_%d", window.GroupID)

	for _, raw := range rawCandidates {
		// 跳过低置信度
		if raw.Confidence < 0.7 {
			continue
		}

		// 映射事件 ID（从窗口索引到真实事件 ID）
		evidenceEventIDs := b.mapEventIDs(raw.EvidenceEventIDs, window)
		if len(evidenceEventIDs) == 0 {
			continue // 没有有效证据，跳过
		}

		// 解析过期时间
		var validUntil *time.Time
		if raw.ValidUntil != nil && *raw.ValidUntil != "" {
			t, err := time.Parse(time.RFC3339, *raw.ValidUntil)
			if err == nil {
				validUntil = &t
			}
		}

		candidate := &memorydomain.MemoryCandidate{
			Scope:            scope,
			SubjectKind:      memorydomain.SubjectKind(raw.SubjectKind),
			SubjectID:        raw.SubjectID,
			Type:             memorydomain.MemoryType(raw.Type),
			Subtype:          raw.Subtype,
			Content:          raw.Content,
			Predicate:        memorydomain.Predicate(raw.Predicate),
			NormalizedValue:  raw.NormalizedValue,
			Qualifier:        raw.Qualifier,
			ValidUntil:       validUntil,
			ParticipantIDs:   raw.ParticipantIDs,
			BotRole:          memorydomain.BotRole(raw.BotRole),
			AnchorEventID:    b.mapEventID(raw.AnchorEventID, window),
			EvidenceEventIDs: evidenceEventIDs,
			SourceRole:       "primary",
			Intent:           "new",
			ExtractorVersion: b.extractorVersion,
			ObservedAt:       time.Now(),
		}

		// 如果缺少 qualifier，设置默认值
		if candidate.Qualifier == "" {
			candidate.Qualifier = "default"
		}

		candidates = append(candidates, candidate)
	}

	return candidates, nil
}

// mapEventID 将窗口中的事件索引映射到真实事件 ID
func (b *ExtractionPromptBuilder) mapEventID(indexStr string, window *Window) string {
	if indexStr == "" {
		return ""
	}

	var index int
	if _, err := fmt.Sscanf(indexStr, "%d", &index); err != nil {
		return indexStr // 如果已经是事件 ID 格式，直接返回
	}

	if index < 0 || index >= len(window.Events) {
		return ""
	}

	return window.Events[index].EventID
}

// mapEventIDs 批量映射事件 ID
func (b *ExtractionPromptBuilder) mapEventIDs(indexStrs []string, window *Window) []string {
	result := make([]string, 0, len(indexStrs))
	for _, indexStr := range indexStrs {
		if eventID := b.mapEventID(indexStr, window); eventID != "" {
			result = append(result, eventID)
		}
	}
	return result
}
