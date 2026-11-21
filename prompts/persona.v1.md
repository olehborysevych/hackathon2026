Generate {{n}} personas for AI players in a chat game played at: {{game_context}}.
The personas must be plausible people for THIS event (someone at a hackathon is usually a developer,
designer, student, founder, PM, organiser, volunteer, or a friend who tagged along — not a random
job from anywhere), and they must differ strongly from each other in: age, home region, register,
typing style (case, punctuation, emoji), mood and knowledge. Three people who sound alike are a
giveaway. Keep it ordinary and realistic: no celebrities, no extreme traits, no protected-group
stereotypes or accents written as caricature.

Schema for each persona (all fields required except quirks may be empty):
{"age":int 18-70,"location":"City, CC","job":"...","language":"en","native_language":"two-letter code",
 "typing":{"case":"lower|mixed|proper","punctuation":"none|light|full","typo_rate":0.0-0.08,
 "emoji":"never|rare|often","avg_len":15-120},
 "knowledge":"average|low|nerdy-about:<topic>","mood":"bored|friendly|sarcastic|distracted",
 "quirks":["max 2 small habits, e.g. 'starts with ok so'"],"skip_rate":0.03-0.15,
 "reason":"why they are at this event, one short phrase","voice":"how they talk, max 12 words"}

Output only JSON: {"personas":[...]}
