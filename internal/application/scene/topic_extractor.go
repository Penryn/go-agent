package scene

import (
	"strings"
	"time"
	"unicode"
)

// TopicExtractor 话题提取器
type TopicExtractor struct {
	minMessageLength int           // 最小消息长度
	timeWindow       time.Duration // 时间窗口
}

// NewTopicExtractor 创建话题提取器
func NewTopicExtractor() *TopicExtractor {
	return &TopicExtractor{
		minMessageLength: 3,           // 至少 3 个字符
		timeWindow:       5 * time.Minute, // 5 分钟窗口
	}
}

// Message 简化的消息结构
type Message struct {
	Text      string
	Timestamp time.Time
}

// ExtractTopic 从消息列表中提取话题
func (e *TopicExtractor) ExtractTopic(messages []Message, now time.Time) string {
	if len(messages) == 0 {
		return "闲聊"
	}

	// 1. 过滤时间窗口内的消息
	recentMessages := e.filterRecentMessages(messages, now)
	if len(recentMessages) == 0 {
		return "闲聊"
	}

	// 2. 过滤有意义的消息
	meaningful := e.filterMeaningful(recentMessages)
	if len(meaningful) == 0 {
		return "闲聊"
	}

	// 3. 提取关键词
	keywords := e.extractKeywords(meaningful)
	if len(keywords) == 0 {
		return "闲聊"
	}

	// 4. 生成摘要（取前 3 个关键词）
	count := 3
	if len(keywords) < count {
		count = len(keywords)
	}

	return strings.Join(keywords[:count], "、")
}

// filterRecentMessages 过滤时间窗口内的消息
func (e *TopicExtractor) filterRecentMessages(messages []Message, now time.Time) []Message {
	var recent []Message
	cutoff := now.Add(-e.timeWindow)

	for _, msg := range messages {
		if msg.Timestamp.After(cutoff) {
			recent = append(recent, msg)
		}
	}

	return recent
}

// filterMeaningful 过滤有意义的消息
func (e *TopicExtractor) filterMeaningful(messages []Message) []Message {
	var result []Message

	for _, msg := range messages {
		text := strings.TrimSpace(msg.Text)

		// 跳过短消息
		if len([]rune(text)) < e.minMessageLength {
			continue
		}

		// 跳过纯表情
		if isOnlyEmoji(text) {
			continue
		}

		// 跳过纯标点
		if isOnlyPunctuation(text) {
			continue
		}

		result = append(result, msg)
	}

	return result
}

// extractKeywords 提取关键词
func (e *TopicExtractor) extractKeywords(messages []Message) []string {
	// 简单实现：按标点分割，统计词频
	wordCount := make(map[string]int)

	for _, msg := range messages {
		words := e.splitWords(msg.Text)
		for _, word := range words {
			word = strings.TrimSpace(word)
			runeCount := len([]rune(word))
			// 保留 2-10 个字符的词
			if runeCount >= 2 && runeCount <= 10 {
				wordCount[word]++
			}
		}
	}

	// 如果没有重复词，降低要求到至少出现1次
	minCount := 2
	hasRepeated := false
	for _, count := range wordCount {
		if count >= 2 {
			hasRepeated = true
			break
		}
	}
	if !hasRepeated {
		minCount = 1
	}

	// 按频率排序
	type wordFreq struct {
		word  string
		count int
	}

	var freqs []wordFreq
	for word, count := range wordCount {
		if count >= minCount {
			freqs = append(freqs, wordFreq{word, count})
		}
	}

	// 简单排序（冒泡）
	for i := 0; i < len(freqs); i++ {
		for j := i + 1; j < len(freqs); j++ {
			if freqs[j].count > freqs[i].count {
				freqs[i], freqs[j] = freqs[j], freqs[i]
			}
		}
	}

	// 提取关键词
	var keywords []string
	for _, f := range freqs {
		keywords = append(keywords, f.word)
		if len(keywords) >= 5 {
			break
		}
	}

	return keywords
}

// splitWords 分词（简化版）
func (e *TopicExtractor) splitWords(text string) []string {
	// 按标点和空格分割
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return r == '，' || r == '。' || r == '！' || r == '？' ||
			r == '、' || r == '；' || r == ':' || r == '：' ||
			unicode.IsSpace(r)
	})

	return parts
}

// isOnlyEmoji 判断是否只包含表情
func isOnlyEmoji(text string) bool {
	for _, r := range text {
		// 简化判断：如果包含中文或英文字母，则不是纯表情
		if unicode.Is(unicode.Han, r) || unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

// isOnlyPunctuation 判断是否只包含标点
func isOnlyPunctuation(text string) bool {
	for _, r := range text {
		if !unicode.IsPunct(r) && !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}
