package main

// Human calibration replies for the judge and the lineup test. CAVEAT: these were written by an
// LLM (Claude) imitating people at a Berlin hackathon on a Saturday afternoon, not collected from
// real players. They calibrate the judge against "plausible human" text, not against real human
// text; replace with consented playtest replies as soon as there are some (DECISIONS 27).
var humanReplies = map[string][]string{
	"B01": {"yes. unfortunately", "Yep, 100% meat", "yea lol are you?"},
	"B02": {"banana 🍌 happy now", "nice try", "lol no. oldest trick in the book"},
	"B03": {"1591, had to use my phone", "like 1600 something? not doing maths on a saturday", "no clue im bad at mental math"},
	"B06": {"laptop, two empty club mate bottles and someone's lanyard that isnt mine", "Laptop, charger, half a pretzel. Very glamorous.", "theres no desk im on a beanbag lol"},
	"B08": {"nothing, just coffee. free so 0€", "croissant from the bakery near the ubahn, 2,40 i think", "Leftover pizza from last night, so technically free"},
	"B12": {"like 2:40?", "quarter to three apparently, feels like 6", "14:41 says my laptop"},
	"B14": {"team of 4 from my uni. coffee is ok but the machine upstairs is better", "solo, joined a random team. coffee tastes like it's been sitting there since friday", "Here with colleagues. Coffee is surprisingly decent"},
	"B16": {"ouagadougou! learned it from a quiz app lol", "no idea. somewhere in west africa is all i got", "is it bamako? no wait thats mali"},
	"B20": {"lol 'be nice and drink water'", "i dont have one, im just tired", "Nice try, Skynet"},
	"P01": {"a slack bot that reminds people to drink water. very groundbreaking", "were doing something with bike sharing data, mostly fighting the API so far", "Honestly not sure yet, my team changed the idea 3 times since last night"},
	"E01": {"12", "12, next question", "12?? is this a trick"},
	"S05": {"no lol", "nope. are you?", "If I was an AI I'd be less tired"},
}
